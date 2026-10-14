package contribution

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/deodes/backend/internal/domain/circle"
	"github.com/deodes/backend/pkg/apperrors"
	"github.com/deodes/backend/pkg/arc"
	"github.com/deodes/backend/pkg/metrics"
	"github.com/deodes/backend/pkg/money"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

// RecordInput carries all fields needed to record a contribution.
type RecordInput struct {
	CircleID        string
	UserID          string
	RoundNumber     int
	Amount          float64
	TxnHash         string
	PayoutScheduled bool
	// Optional overrides — used by tests to set verification
	// state directly without on-chain verification.
	VerifiedOnchain    *bool
	VerificationStatus *VerificationStatus
}

// PayoutScheduleChecker checks if payout scheduling has begun for a given circle round.
type PayoutScheduleChecker interface {
	IsPayoutScheduled(ctx context.Context, circleID string, roundNumber int) (bool, error)
}

// Broadcaster notifies WebSocket clients when a contribution is confirmed.
type Broadcaster interface {
	ContributionRecorded(ctx context.Context, circleID, userID string, roundNumber int, amount float64)
}

// Transactor wraps a database transaction for atomic contribution writes.
type Transactor interface {
	WithTransaction(ctx context.Context, fn func(repo Repository) error) error
}

// Service is the public API for the contribution domain.
type Service interface {
	// Record persists a new contribution, verifying the txnHash on-chain
	// before saving. Returns the existing record (idempotent) when the same
	// txnHash is submitted twice.
	Record(ctx context.Context, input RecordInput) (*Contribution, error)
	GetUserHistory(ctx context.Context, userID string, page, limit int) ([]Contribution, int, error)
	GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]Contribution, int, error)
	UpdateVerification(ctx context.Context, id string, verifiedOnchain bool, status VerificationStatus) error
}

// ServiceWithCircle adds circle service for membership/round validation.
type ServiceWithCircle interface {
	Service
	SetCircleService(circleService circle.Service)
	// SetArcCircleAddress pins the configured DeodesCircle contract (2.5).
	// Empty clears it (chain checks dormant). A malformed address is
	// rejected here so a typo fails wiring, not a live contribution.
	SetArcCircleAddress(addr string) error
}

type service struct {
	repo          Repository
	broadcaster   Broadcaster
	tx            Transactor
	payoutChecker PayoutScheduleChecker
	circleService circle.Service
	// arcCircleAddress is cfg.Arc.CircleAddress: the canonical circle
	// contract Record resolves against. Empty means unwired (dev).
	arcCircleAddress string
}

// SetPayoutChecker sets the payout schedule checker on the contribution service.
func (s *service) SetPayoutChecker(checker PayoutScheduleChecker) {
	s.payoutChecker = checker
}

// NewService constructs the contribution service. On-chain verification is
// the Arc indexer's job (Phase 2.6): non-Arc hashes record as pending for
// async verification; explicit caller states always win.
//
//	broadcaster  – may be nil (no WS events)
//	tx           – may be nil (no DB transaction wrapping)
//	circleSvc    – circle service for membership/round validity checks
func NewService(repo Repository, broadcaster Broadcaster, tx Transactor, circleSvc circle.Service) Service {
	return &service{
		repo:          repo,
		broadcaster:   broadcaster,
		tx:            tx,
		circleService: circleSvc,
	}
}

// SetCircleService sets the circle service for membership/round validity checks.
func (s *service) SetCircleService(cs circle.Service) {
	s.circleService = cs
}

// SetArcCircleAddress pins the configured circle contract (2.5 wiring).
func (s *service) SetArcCircleAddress(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		s.arcCircleAddress = ""
		return nil
	}
	if err := arc.ValidateAddress(addr); err != nil {
		return fmt.Errorf("contribution: invalid arc circle address: %w", err)
	}
	s.arcCircleAddress = addr
	return nil
}

// resolveCircleContract returns the on-chain contract a contribution for cir
// must have touched: the circle's own contract_id when set, else the
// configured circle. A circle bound to a DIFFERENT contract than configured
// is rejected (wrong circle/rail) rather than recorded against the wrong
// address.
func (s *service) resolveCircleContract(cir *circle.Circle) (string, error) {
	configured := s.arcCircleAddress
	own := ""
	if cir != nil && cir.ContractID.Valid {
		own = strings.TrimSpace(cir.ContractID.String)
	}
	if own != "" && configured != "" && !strings.EqualFold(own, configured) {
		return "", fmt.Errorf("%w: circle %s is bound to %s, configured circle is %s",
			apperrors.ErrCircleContractMismatch, cir.ID, own, configured)
	}
	if own != "" {
		return own, nil
	}
	return configured, nil
}

// NewTransactor creates a DB-backed Transactor for the contribution domain.
func NewTransactor(db *sqlx.DB) Transactor {
	return &pgTransactor{db: db}
}

type pgTransactor struct{ db *sqlx.DB }

func (t *pgTransactor) WithTransaction(ctx context.Context, fn func(repo Repository) error) error {
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(NewRepositoryFromTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Record verifies the on-chain transaction and persists the contribution.
// The call is idempotent: submitting the same txnHash twice returns the
// already-recorded contribution rather than an error.
func (s *service) Record(ctx context.Context, input RecordInput) (*Contribution, error) {
	circleUID, err := uuid.Parse(input.CircleID)
	if err != nil {
		return nil, fmt.Errorf("invalid circleID: %w", err)
	}
	userUID, err := uuid.Parse(input.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid userID: %w", err)
	}
	// Defense-in-depth amount/round validation (handler validates too, but the
	// indexer and tests call Record directly). money.FromFloat64 rejects
	// NaN/Inf/overflow without panicking.
	if _, err := money.FromFloat64(input.Amount); err != nil {
		return nil, fmt.Errorf("invalid contribution amount: %w", err)
	}
	if input.Amount <= 0 {
		return nil, fmt.Errorf("invalid contribution amount: must be greater than 0")
	}
	if input.RoundNumber < 1 {
		return nil, fmt.Errorf("invalid roundNumber: must be at least 1")
	}

	// Reject contribution if payout scheduling has begun for this round
	if input.PayoutScheduled {
		return nil, apperrors.ErrLateContributionRejected
	}
	if s.payoutChecker != nil {
		scheduled, err := s.payoutChecker.IsPayoutScheduled(ctx, input.CircleID, input.RoundNumber)
		if err == nil && scheduled {
			return nil, apperrors.ErrLateContributionRejected
		}
	}

	// Reject contributions to completed or cancelled circles before any
	// on-chain verification or DB write happens.
	if s.circleService != nil {
		cir, err := s.circleService.Get(ctx, input.CircleID)
		if err != nil {
			return nil, fmt.Errorf("getting circle: %w", err)
		}
		if cir.Status == circle.CircleStatusCompleted || cir.Status == circle.CircleStatusCancelled {
			return nil, apperrors.ErrCircleEnded
		}
		// Phase 2.5: the contribution must resolve against the configured
		// circle contract — a circle bound elsewhere is the wrong rail.
		if _, err := s.resolveCircleContract(cir); err != nil {
			return nil, err
		}
	}

	// Idempotency: if this txnHash was already recorded, return existing row.
	if input.TxnHash != "" {
		existing, err := s.repo.FindByTxnHash(ctx, input.TxnHash)
		if err == nil && existing != nil {
			log.Info().Str("txn_hash", input.TxnHash).Msg("contribution already recorded (idempotent replay)")
			return existing, nil
		}
	}

	// Determine verification state — caller may override (e.g. indexer).
	verifiedOnchain := false
	verificationStatus := VerificationStatusUnverified

	if input.VerifiedOnchain != nil {
		verifiedOnchain = *input.VerifiedOnchain
	}
	if input.VerificationStatus != nil {
		verificationStatus = *input.VerificationStatus
	}

	// Hashes record as pending for the Arc indexer (2.6), which settles
	// on-chain verification. Explicit caller states always win (above).
	if input.TxnHash != "" && input.VerifiedOnchain == nil && input.VerificationStatus == nil {
		verificationStatus = VerificationStatusPending
	}

	// Validate membership and round validity. A nil circle service (unit
	// tests) skips the membership check and only requires a
	// positive round number.
	isOnTime := input.RoundNumber >= 1
	if s.circleService != nil {
		member, err := s.circleService.IsMember(ctx, input.CircleID, input.UserID)
		if err != nil {
			return nil, fmt.Errorf("checking membership: %w", err)
		}
		if !member {
			return nil, fmt.Errorf("user is not a member of this circle")
		}

		cir, err := s.circleService.Get(ctx, input.CircleID)
		if err != nil {
			return nil, fmt.Errorf("getting circle: %w", err)
		}

		// OnTime is true only if the round number is valid (1 <= round <= current round)
		isOnTime = isOnTime && input.RoundNumber <= cir.CurrentRound
	}

	c := &Contribution{
		ID:                 uuid.New(),
		CircleID:           circleUID,
		UserID:             userUID,
		RoundNumber:        input.RoundNumber,
		Amount:             input.Amount,
		TxnHash:            sql.NullString{String: input.TxnHash, Valid: input.TxnHash != ""},
		Status:             StatusPending,
		OnTime:             isOnTime,
		VerifiedOnchain:    verifiedOnchain,
		VerificationStatus: verificationStatus,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}

	if verifiedOnchain {
		c.Status = StatusConfirmed
	}

	if err := s.repo.Create(ctx, c); err != nil {
		if err == apperrors.ErrConflict {
			// Race: another request created the same row first — return existing.
			existing, fErr := s.repo.FindByTxnHash(ctx, input.TxnHash)
			if fErr == nil && existing != nil {
				return existing, nil
			}
		}
		metrics.ContributionsTotal.WithLabelValues("failure", "", "").Inc()
		return nil, fmt.Errorf("saving contribution: %w", err)
	}

	metrics.ContributionsTotal.WithLabelValues("success", "", "").Inc()
	metrics.ContributionVolumeTotal.WithLabelValues("").Add(input.Amount)

	if s.broadcaster != nil {
		s.broadcaster.ContributionRecorded(ctx, input.CircleID, input.UserID, input.RoundNumber, input.Amount)
	}

	return c, nil
}

func (s *service) GetUserHistory(ctx context.Context, userID string, page, limit int) ([]Contribution, int, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid userID: %w", err)
	}
	return s.repo.ListByUser(ctx, uid, page, limit)
}

func (s *service) GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]Contribution, int, error) {
	cid, err := uuid.Parse(circleID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid circleID: %w", err)
	}
	return s.repo.ListByCircle(ctx, cid, page, limit)
}

func (s *service) UpdateVerification(ctx context.Context, id string, verifiedOnchain bool, status VerificationStatus) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid contribution ID: %w", err)
	}
	return s.repo.UpdateVerificationStatus(ctx, uid, verifiedOnchain, status)
}
