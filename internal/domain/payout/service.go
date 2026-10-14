package payout

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/deodes/backend/internal/domain/circle"
	"github.com/deodes/backend/pkg/apperrors"
	"github.com/deodes/backend/pkg/metrics"
	"github.com/deodes/backend/pkg/money"
	"github.com/rs/zerolog/log"
)

// RecordInput carries all fields needed to record a payout.
// Validation tags mirror the TriggerPayout request contract: the handler
// binds the JSON body directly into this struct and validates it before the
// circle ID from the URL path is applied.
type RecordInput struct {
	CircleID    string     `json:"-" validate:"required,uuid"`
	RecipientID string     `json:"recipientId" validate:"required,uuid"`
	RoundNumber int        `json:"roundNumber" validate:"required,gte=1"`
	Amount      float64    `json:"amount" validate:"required,gt=0"`
	FeeAmount   float64    `json:"feeAmount" validate:"gte=0"`
	TxnHash     string     `json:"txnHash" validate:"required"`
	PayoutType  PayoutType `json:"payoutType" validate:"required,oneof=random fixed auction vote"`
	// Optional overrides — used by the indexer / tests to set verification
	// state directly without going through the Horizon check.
	VerifiedOnchain    *bool
	VerificationStatus *VerificationStatus
}

// HorizonVerifier is satisfied by *stellar.Client.
type HorizonVerifier interface {
	// VerifyPayment checks that txnHash exists, was successful, and has a
	// payment TO expectedTo for expectedAmount.
	VerifyPayment(ctx context.Context, txnHash, expectedTo, expectedAmount string) (bool, error)
}

// WalletLookup resolves a user's on-chain Stellar wallet address by their UUID.
// Implemented in main.go by a thin shim over user.Repository to avoid a
// direct dependency on the user domain.
type WalletLookup interface {
	WalletAddressForUser(ctx context.Context, userID uuid.UUID) (string, error)
}

// Service is the public API for the payout domain.
type Service interface {
	// Record persists a new payout, verifying the txnHash on-chain before
	// saving. Idempotent: the same txnHash submitted twice returns the
	// already-recorded payout.
	Record(ctx context.Context, input RecordInput) (*Payout, error)
	GetUserHistory(ctx context.Context, userID string, page, limit int) ([]Payout, int, error)
	GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]Payout, int, error)
	// GetRoundPayout returns the payout recorded for a circle round, or nil
	// when the round has not been distributed yet.
	GetRoundPayout(ctx context.Context, circleID string, round int) (*Payout, error)
	UpdateVerification(ctx context.Context, id string, verifiedOnchain bool, status VerificationStatus) error
}

// ServiceWithCircle adds circle service for membership/round validation.
type ServiceWithCircle interface {
	Service
	SetCircleService(circleService circle.Service)
}

type service struct {
	repo          Repository
	horizon       HorizonVerifier
	walletLookup  WalletLookup
	circleService circle.Service
}

// NewService constructs the payout service.
//
//	horizon      – may be nil (on-chain verification skipped; useful in tests)
//	walletLookup – may be nil (recipient wallet check skipped)
//	circleSvc    – circle service for membership/round validity checks
//
// The third parameter is typed as interface{} so that callers can pass a
// concrete *user.pgRepo (which satisfies WalletLookup when wrapped) or nil.
// If it already satisfies WalletLookup it is used directly; otherwise it is
// ignored.
func NewService(repo Repository, horizon HorizonVerifier, walletLookup interface{}, circleSvc circle.Service) Service {
	var wl WalletLookup
	if walletLookup != nil {
		if v, ok := walletLookup.(WalletLookup); ok {
			wl = v
		}
		// If it doesn't satisfy WalletLookup (e.g. bare *user.pgRepo),
		// main.go must wrap it with a NewWalletLookupAdapter first.
	}
	return &service{
		repo:          repo,
		horizon:       horizon,
		walletLookup:  wl,
		circleService: circleSvc,
	}
}

// Record verifies the on-chain transaction and persists the payout.
// The call is idempotent: submitting the same txnHash twice returns the
// already-recorded payout rather than an error.
func (s *service) Record(ctx context.Context, input RecordInput) (*Payout, error) {
	circleUID, err := uuid.Parse(input.CircleID)
	if err != nil {
		return nil, fmt.Errorf("invalid circleID: %w", err)
	}
	recipientUID, err := uuid.Parse(input.RecipientID)
	if err != nil {
		return nil, fmt.Errorf("invalid recipientID: %w", err)
	}

	// Defensive validation for callers that bypass the HTTP handler
	// (indexer replay, keeper jobs). The handler enforces the same rules
	// via validator tags on RecordInput.
	if input.RoundNumber < 1 {
		return nil, fmt.Errorf("invalid round number %d: must be at least 1", input.RoundNumber)
	}
	if input.Amount <= 0 {
		return nil, fmt.Errorf("invalid payout amount %v: must be greater than zero", input.Amount)
	}
	if input.FeeAmount < 0 {
		return nil, fmt.Errorf("invalid fee amount %v: must not be negative", input.FeeAmount)
	}
	if input.FeeAmount > input.Amount {
		return nil, fmt.Errorf("invalid fee amount %v: exceeds payout amount %v", input.FeeAmount, input.Amount)
	}

	// Idempotency: return existing record if this txnHash was already processed.
	if input.TxnHash != "" {
		existing, err := s.repo.FindByTxnHash(ctx, input.TxnHash)
		if err == nil && existing != nil {
			log.Info().Str("txn_hash", input.TxnHash).Msg("payout already recorded (idempotent replay)")
			return existing, nil
		}
	}

	// Validate membership and round validity. A nil circle service (unit
	// tests, indexer replay) skips the membership check and only requires a
	// positive round number.
	isOnTime := input.RoundNumber >= 1
	if s.circleService != nil {
		member, err := s.circleService.IsMember(ctx, input.CircleID, input.RecipientID)
		if err != nil {
			return nil, fmt.Errorf("checking membership: %w", err)
		}
		if !member {
			return nil, fmt.Errorf("recipient is not a member of this circle")
		}

		cir, err := s.circleService.Get(ctx, input.CircleID)
		if err != nil {
			return nil, fmt.Errorf("getting circle: %w", err)
		}

		// OnTime is true only if the round number is valid (1 <= round <= current round)
		isOnTime = isOnTime && input.RoundNumber <= cir.CurrentRound
	}

	// Rotation guard (RULE 2 — random payouts rotate without repeat). When
	// the circle service is available, a random-rotation payout to a member
	// who has already been paid in this circle is rejected: a full rotation
	// pays every member exactly once, and a new rotation only starts after
	// everyone has been paid. Non-random payout types (fixed/auction/vote)
	// keep their own selection semantics and are unaffected.
	if input.PayoutType == PayoutTypeRandom && s.circleService != nil {
		alreadyPaid, err := s.circlePaidRecipients(ctx, circleUID)
		if err != nil {
			return nil, fmt.Errorf("checking rotation state: %w", err)
		}
		if alreadyPaid[recipientUID] {
			return nil, ErrRecipientAlreadyPaid
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

	// On-chain verification: only run when horizon is available and the caller
	// has not already provided an explicit verification state.
	if s.horizon != nil && input.TxnHash != "" &&
		input.VerifiedOnchain == nil && input.VerificationStatus == nil {

		recipientWallet, walletErr := s.resolveRecipientWallet(ctx, recipientUID)
		if walletErr != nil {
			// Cannot resolve wallet — record as pending for async retry.
			log.Warn().Err(walletErr).Str("recipient_id", input.RecipientID).
				Msg("could not resolve recipient wallet; recording payout as pending")
			verificationStatus = VerificationStatusPending
		} else {
			amount, amtErr := money.FromFloat64(input.Amount)
			if amtErr != nil {
				return nil, fmt.Errorf("invalid payout amount: %w", amtErr)
			}
			ok, verErr := s.horizon.VerifyPayment(ctx, input.TxnHash, recipientWallet, amount.String())
			if verErr != nil {
				log.Warn().Err(verErr).Str("txn_hash", input.TxnHash).
					Msg("horizon verification failed; recording payout as pending")
				verificationStatus = VerificationStatusPending
			} else if !ok {
				metrics.PayoutsTotal.WithLabelValues("rejected", "", "").Inc()
				return nil, fmt.Errorf("on-chain verification failed: txnHash %s does not match recipient/amount", input.TxnHash)
			} else {
				verifiedOnchain = true
				verificationStatus = VerificationStatusVerified
			}
		}
	}

	// Guard against duplicate payouts in the same circle/round. A ROSCA round
	// distributes exactly once, regardless of recipient: a second trigger for
	// an already-paid round is a conflict. The lookup is by (circle, round)
	// directly, so — unlike the previous bounded ListByCircle scan — it cannot
	// miss rows past a page limit.
	if existing, err := s.repo.FindByCircleAndRound(ctx, circleUID, input.RoundNumber); err == nil && existing != nil {
		log.Warn().Str("circle_id", input.CircleID).
			Int("round", input.RoundNumber).
			Msg("payout already exists for this circle round")
		return existing, ErrRoundAlreadyPaid
	}

	p := &Payout{
		ID:                 uuid.New(),
		CircleID:           circleUID,
		RecipientID:        recipientUID,
		RoundNumber:        input.RoundNumber,
		Amount:             input.Amount,
		FeeAmount:          input.FeeAmount,
		TxnHash:            sql.NullString{String: input.TxnHash, Valid: input.TxnHash != ""},
		PayoutType:         input.PayoutType,
		VerifiedOnchain:    verifiedOnchain,
		VerificationStatus: verificationStatus,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		OnTime:             isOnTime,
	}

	if err := s.repo.Create(ctx, p); err != nil {
		if err == apperrors.ErrConflict {
			// Race: another request created the same row — return existing.
			if input.TxnHash != "" {
				if got, fErr := s.repo.FindByTxnHash(ctx, input.TxnHash); fErr == nil && got != nil {
					return got, nil
				}
			}
		}
		metrics.PayoutsTotal.WithLabelValues("failure", "", "").Inc()
		return nil, fmt.Errorf("saving payout: %w", err)
	}

	metrics.PayoutsTotal.WithLabelValues("success", "", "").Inc()
	metrics.PayoutVolumeTotal.WithLabelValues("").Add(input.Amount)
	return p, nil
}

func (s *service) resolveRecipientWallet(ctx context.Context, recipientUID uuid.UUID) (string, error) {
	if s.walletLookup == nil {
		return "", fmt.Errorf("no wallet lookup available")
	}
	return s.walletLookup.WalletAddressForUser(ctx, recipientUID)
}

// circlePaidRecipients returns the set of user IDs that have received at
// least one payout in the circle. It pages through the full history —
// ListByCircle is capped at 100 rows per page — so a repeat is never missed
// past a page boundary.
func (s *service) circlePaidRecipients(ctx context.Context, circleID uuid.UUID) (map[uuid.UUID]bool, error) {
	const pageSize = 100
	paid := make(map[uuid.UUID]bool)
	for page := 1; ; page++ {
		payouts, total, err := s.repo.ListByCircle(ctx, circleID, page, pageSize)
		if err != nil {
			return nil, err
		}
		for _, p := range payouts {
			paid[p.RecipientID] = true
		}
		if len(payouts) == 0 || page*pageSize >= total {
			break
		}
	}
	return paid, nil
}

func (s *service) GetUserHistory(ctx context.Context, userID string, page, limit int) ([]Payout, int, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid userID: %w", err)
	}
	return s.repo.ListByUser(ctx, uid, page, limit)
}

func (s *service) GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]Payout, int, error) {
	cid, err := uuid.Parse(circleID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid circleID: %w", err)
	}
	return s.repo.ListByCircle(ctx, cid, page, limit)
}

// GetRoundPayout returns the payout recorded for a circle round, or nil when
// the round has not been distributed yet.
func (s *service) GetRoundPayout(ctx context.Context, circleID string, round int) (*Payout, error) {
	cid, err := uuid.Parse(circleID)
	if err != nil {
		return nil, fmt.Errorf("invalid circleID: %w", err)
	}
	existing, err := s.repo.FindByCircleAndRound(ctx, cid, round)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return existing, nil
}

func (s *service) UpdateVerification(ctx context.Context, id string, verifiedOnchain bool, status VerificationStatus) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid payout ID: %w", err)
	}
	return s.repo.UpdateVerificationStatus(ctx, uid, verifiedOnchain, status)
}

// SetCircleService sets the circle service for membership/round validity checks.
func (s *service) SetCircleService(cs circle.Service) {
	s.circleService = cs
}
