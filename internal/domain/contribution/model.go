package contribution

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ContributionStatus string

const (
	StatusPending   ContributionStatus = "pending"
	StatusConfirmed ContributionStatus = "confirmed"
	StatusFailed    ContributionStatus = "failed"
	StatusLate      ContributionStatus = "late"
)

type VerificationStatus string

const (
	VerificationStatusUnverified VerificationStatus = "unverified"
	VerificationStatusPending    VerificationStatus = "pending"
	VerificationStatusVerified   VerificationStatus = "verified"
	VerificationStatusFailed     VerificationStatus = "failed"
)

type Contribution struct {
	ID                 uuid.UUID          `json:"id" db:"id"`
	CircleID           uuid.UUID          `json:"circleId" db:"circle_id"`
	UserID             uuid.UUID          `json:"userId" db:"user_id"`
	RoundNumber        int                `json:"roundNumber" db:"round_number"`
	Amount             float64            `json:"amount" db:"amount"`
	TxnHash            sql.NullString     `json:"-" db:"txn_hash"`
	Status             ContributionStatus `json:"status" db:"status"`
	OnTime             bool               `json:"onTime" db:"on_time"`
	VerifiedOnchain    bool               `json:"verifiedOnchain" db:"verified_onchain"`
	VerificationStatus VerificationStatus `json:"verificationStatus" db:"verification_status"`
	CreatedAt          time.Time          `json:"createdAt" db:"created_at"`
	UpdatedAt          time.Time          `json:"updatedAt" db:"updated_at"`
}

// contributionJSON is the wire shape. TxnHash is emitted as a plain
// string-or-null so EVM (0x…) hashes arrive as strings instead of the
// sql.NullString object {String,Valid} that encoding/json produces by
// default. The frontend Contribution type expects string|null.
type contributionJSON struct {
	ID                 uuid.UUID          `json:"id"`
	CircleID           uuid.UUID          `json:"circleId"`
	UserID             uuid.UUID          `json:"userId"`
	RoundNumber        int                `json:"roundNumber"`
	Amount             float64            `json:"amount"`
	TxnHash            *string            `json:"txnHash,omitempty"`
	Status             ContributionStatus `json:"status"`
	OnTime             bool               `json:"onTime"`
	VerifiedOnchain    bool               `json:"verifiedOnchain"`
	VerificationStatus VerificationStatus `json:"verificationStatus"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
}

// MarshalJSON emits txnHash as string|null for Arc EVM compatibility.
func (c Contribution) MarshalJSON() ([]byte, error) {
	return marshalContribution(c)
}

func marshalContribution(c Contribution) ([]byte, error) {
	out := contributionJSON{
		ID:                 c.ID,
		CircleID:           c.CircleID,
		UserID:             c.UserID,
		RoundNumber:        c.RoundNumber,
		Amount:             c.Amount,
		Status:             c.Status,
		OnTime:             c.OnTime,
		VerifiedOnchain:    c.VerifiedOnchain,
		VerificationStatus: c.VerificationStatus,
		CreatedAt:          c.CreatedAt,
		UpdatedAt:          c.UpdatedAt,
	}
	if c.TxnHash.Valid {
		s := c.TxnHash.String
		out.TxnHash = &s
	}
	// Use an alias to avoid infinite recursion.
	type plain contributionJSON
	return json.Marshal(plain(out))
}
