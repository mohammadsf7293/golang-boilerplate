package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
	"github.com/mohammadsf7293/golang-boilerplate/internal/metrics"
)

// Relay publishes unpublished outbox rows to the message broker.
type Relay struct {
	storage   domain.Storage
	queue     domain.Queue
	batchSize int32
	interval  time.Duration
}

func NewRelay(storage domain.Storage, queue domain.Queue, batchSize int32, interval time.Duration) *Relay {
	if batchSize <= 0 {
		batchSize = 50
	}
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	return &Relay{
		storage:   storage,
		queue:     queue,
		batchSize: batchSize,
		interval:  interval,
	}
}

func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	slog.Info("outbox relay started", "interval", r.interval, "batch_size", r.batchSize)
	for {
		select {
		case <-ctx.Done():
			slog.Info("outbox relay stopped")
			return
		case <-ticker.C:
			r.PublishOnce(ctx)
		}
	}
}

// PublishOnce publishes currently unpublished outbox entries up to batchSize.
func (r *Relay) PublishOnce(ctx context.Context) {
	entries, err := r.storage.GetUnpublishedOutbox(ctx, r.batchSize)
	if err != nil {
		slog.Error("failed to fetch unpublished outbox entries", "error", err)
		metrics.OutboxPublishFailures.Inc()
		return
	}

	metrics.OutboxBacklog.Set(float64(len(entries)))
	if len(entries) == 0 {
		return
	}

	for _, entry := range entries {
		if err := r.queue.PublishMessage(entry.QueueName, entry.Payload, entry.MessageID); err != nil {
			slog.Error("failed to publish outbox entry", "error", err, "outbox_id", entry.ID, "message_id", entry.MessageID)
			metrics.OutboxPublishFailures.Inc()
			continue
		}
		if err := r.storage.MarkOutboxPublished(ctx, entry.ID); err != nil {
			slog.Error("failed to mark outbox entry published", "error", err, "outbox_id", entry.ID)
			metrics.OutboxPublishFailures.Inc()
			continue
		}
		metrics.OutboxPublishedTotal.Inc()
	}
}
