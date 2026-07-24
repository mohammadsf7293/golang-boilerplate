package outbox_test

import (
	"context"
	"testing"

	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
	"github.com/mohammadsf7293/golang-boilerplate/internal/outbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStorage struct {
	domain.Storage
	entries   []*domain.OutboxEntry
	published []int64
}

func (f *fakeStorage) GetUnpublishedOutbox(ctx context.Context, limit int32) ([]*domain.OutboxEntry, error) {
	if int(limit) < len(f.entries) {
		return f.entries[:limit], nil
	}
	return f.entries, nil
}

func (f *fakeStorage) MarkOutboxPublished(ctx context.Context, id int64) error {
	f.published = append(f.published, id)
	remaining := make([]*domain.OutboxEntry, 0, len(f.entries))
	for _, e := range f.entries {
		if e.ID != id {
			remaining = append(remaining, e)
		}
	}
	f.entries = remaining
	return nil
}

type fakeQueue struct {
	published []string
	failIDs   map[string]bool
}

func (q *fakeQueue) IsHealthy() bool { return true }
func (q *fakeQueue) Close() error    { return nil }
func (q *fakeQueue) ConsumeMessages(consumerName, queueName string, handler domain.MessageHandler) error {
	return nil
}
func (q *fakeQueue) PublishMessage(queueName, body, messageID string) error {
	if q.failIDs[messageID] {
		return assert.AnError
	}
	q.published = append(q.published, messageID)
	return nil
}

func TestRelayPublishOnce(t *testing.T) {
	storage := &fakeStorage{
		entries: []*domain.OutboxEntry{
			{ID: 1, MessageID: "m1", QueueName: "q1", Payload: `{"id":1}`},
			{ID: 2, MessageID: "m2", QueueName: "q1", Payload: `{"id":2}`},
		},
	}
	queue := &fakeQueue{failIDs: map[string]bool{}}
	relay := outbox.NewRelay(storage, queue, 10, 0)

	relay.PublishOnce(context.Background())

	assert.Equal(t, []string{"m1", "m2"}, queue.published)
	assert.Equal(t, []int64{1, 2}, storage.published)
	assert.Empty(t, storage.entries)
}

func TestRelaySkipsFailedPublish(t *testing.T) {
	storage := &fakeStorage{
		entries: []*domain.OutboxEntry{
			{ID: 1, MessageID: "m1", QueueName: "q1", Payload: `{"id":1}`},
			{ID: 2, MessageID: "m2", QueueName: "q1", Payload: `{"id":2}`},
		},
	}
	queue := &fakeQueue{failIDs: map[string]bool{"m1": true}}
	relay := outbox.NewRelay(storage, queue, 10, 0)

	relay.PublishOnce(context.Background())

	require.Equal(t, []string{"m2"}, queue.published)
	assert.Equal(t, []int64{2}, storage.published)
	require.Len(t, storage.entries, 1)
	assert.Equal(t, "m1", storage.entries[0].MessageID)
}
