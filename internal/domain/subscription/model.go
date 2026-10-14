package subscription

import "time"

// Subscription statuses. Only 'active' rows are ever debited by the keeper.
const (
	StatusActive    = "active"
	StatusPaused    = "paused"
	StatusCancelled = "cancelled"
	StatusCompleted = "completed"
)

// Subscription authorizes scheduled auto-debits of one member's contribution
// for one circle. Amounts are decimal strings at the API boundary and NUMERIC
// in storage; arithmetic stays in NUMERIC/off-chain decimal code, never float.
type Subscription struct {
	ID             string     `json:"id" db:"id"`
	UserID         string     `json:"userId" db:"user_id"`
	CircleID       string     `json:"circleId" db:"circle_id"`
	Amount         string     `json:"amount" db:"amount"`
	CapTotal       string     `json:"capTotal" db:"cap_total"`
	CollectedTotal string     `json:"collectedTotal" db:"collected_total"`
	Status         string     `json:"status" db:"status"`
	NextDebitAt    time.Time  `json:"nextDebitAt" db:"next_debit_at"`
	LastDebitAt    *time.Time `json:"lastDebitAt,omitempty" db:"last_debit_at"`
	LastRemindedAt *time.Time `json:"lastRemindedAt,omitempty" db:"last_reminded_at"`
	LastRound      int        `json:"lastRound" db:"last_round"`
	CreatedAt      time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time  `json:"updatedAt" db:"updated_at"`
}

// CreateInput carries the member's consent: fixed amount per debit and a hard
// total cap. NextDebitAt is computed from the circle start/frequency by the
// service layer, not the caller.
type CreateInput struct {
	UserID      string
	CircleID    string
	Amount      string
	CapTotal    string
	NextDebitAt time.Time
}
