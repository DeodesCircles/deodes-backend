package subscription

import (
	"context"
	"strconv"
	"time"

	"github.com/deodes/backend/internal/domain/notification"
)

// notifier adapts *notification.Service to the keeper's Notifier port,
// mapping debit lifecycle events onto the existing contribution notification
// types (inapp channel; email/push follow user preferences downstream).
type notifier struct {
	svc notification.Service
}

// NewNotifier wraps svc. A nil svc yields a no-op notifier so the keeper
// stays testable without the full notification stack.
func NewNotifier(svc notification.Service) Notifier {
	if svc == nil {
		return nilNotifier{}
	}
	return &notifier{svc: svc}
}

type nilNotifier struct{}

func (nilNotifier) NotifyContributionDue(ctx context.Context, userID, circleID, amount string, dueIn time.Duration) error {
	return nil
}
func (nilNotifier) NotifyDebited(ctx context.Context, userID, circleID, amount string, round int, txHash string) error {
	return nil
}
func (nilNotifier) NotifyDebitFailed(ctx context.Context, userID, circleID, amount string, round int, reason string) error {
	return nil
}

func (n *notifier) NotifyContributionDue(ctx context.Context, userID, circleID, amount string, dueIn time.Duration) error {
	hours := int(dueIn.Hours())
	if hours < 1 {
		hours = 1
	}
	_, err := n.svc.Create(ctx, notification.CreateInput{
		UserID:  userID,
		Type:    notification.TypeContributionDue,
		Title:   "Auto-debit tomorrow",
		Body:    "Your " + amount + " USDC contribution debits in about " + strconv.Itoa(hours) + "h. Top up if your balance is short — Deodes never overdrafts.",
		Channel: notification.ChannelInApp,
	})
	return err
}

func (n *notifier) NotifyDebited(ctx context.Context, userID, circleID, amount string, round int, txHash string) error {
	_, err := n.svc.Create(ctx, notification.CreateInput{
		UserID:  userID,
		Type:    notification.TypeContributionReceived,
		Title:   "Contribution collected",
		Body:    "Your " + amount + " USDC auto-debit for round " + strconv.Itoa(round) + " is confirmed. Streak kept alive.",
		Channel: notification.ChannelInApp,
	})
	return err
}

func (n *notifier) NotifyDebitFailed(ctx context.Context, userID, circleID, amount string, round int, reason string) error {
	_, err := n.svc.Create(ctx, notification.CreateInput{
		UserID:  userID,
		Type:    notification.TypeContributionLate,
		Title:   "Auto-debit failed",
		Body:    "Round " + strconv.Itoa(round) + " (" + amount + " USDC) could not be collected: " + reason + ". Pay manually before grace ends to avoid a strike.",
		Channel: notification.ChannelInApp,
	})
	return err
}
