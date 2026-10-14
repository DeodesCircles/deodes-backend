package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ContributionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "deodes",
			Name:      "contributions_total",
			Help:      "Total number of contribution transactions.",
		},
		[]string{"status", "currency", "method"},
	)

	ContributionVolumeTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "deodes",
			Name:      "contribution_volume_total",
			Help:      "Total volume of contributions.",
		},
		[]string{"currency"},
	)

	PayoutsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "deodes",
			Name:      "payouts_total",
			Help:      "Total number of payout transactions.",
		},
		[]string{"status", "currency", "method"},
	)

	PayoutVolumeTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "deodes",
			Name:      "payout_volume_total",
			Help:      "Total volume of payouts.",
		},
		[]string{"currency"},
	)

	UserRegistrationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "deodes",
			Name:      "user_registrations_total",
			Help:      "Total number of user registrations.",
		},
		[]string{"method"},
	)

	ActiveUsersGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "deodes",
			Name:      "active_users_current",
			Help:      "Current number of active users.",
		},
	)

	// Phase 2.6: Arc indexer lag + throughput. Lag is latest finalized
	// block minus the poller cursor; alert on growth (stall_threshold).
	IndexerLagBlocks = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "deodes",
			Name:      "indexer_lag_blocks",
			Help:      "Arc indexer lag in blocks (latest finalized minus cursor).",
		},
		[]string{"chain_id"},
	)

	IndexerEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "deodes",
			Name:      "indexer_events_total",
			Help:      "Decoded Arc events by outcome (persisted = new row, replay = dedup hit, skipped = foreign log).",
		},
		[]string{"event", "outcome"},
	)

	IndexerDeadletterTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "deodes",
			Name:      "indexer_deadletter_total",
			Help:      "Items parked in arc_deadletter by stage.",
		},
		[]string{"stage"},
	)
)
