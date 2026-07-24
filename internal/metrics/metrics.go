package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	TasksCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "tasks_created_total",
		Help: "Total number of tasks created via the API",
	})

	TasksProcessedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tasks_processed_total",
		Help: "Total number of tasks processed by workers",
	}, []string{"result"})

	TaskProcessingDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "task_processing_duration_seconds",
		Help:    "Duration of task processing",
		Buckets: prometheus.DefBuckets,
	})

	OutboxPublishedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "outbox_published_total",
		Help: "Total number of outbox messages published successfully",
	})

	OutboxPublishFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "outbox_publish_failures_total",
		Help: "Total number of outbox publish failures",
	})

	OutboxBacklog = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_backlog",
		Help: "Approximate number of unpublished outbox entries in the last poll",
	})

	InboxDuplicatesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "inbox_duplicates_total",
		Help: "Total number of duplicate messages skipped via inbox",
	})

	LockContentionTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lock_contention_total",
		Help: "Total number of times a worker failed to acquire a task lock",
	})
)
