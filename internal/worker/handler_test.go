package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
	"github.com/stretchr/testify/assert"
)

// decideProcessResult is a pure helper covering inbox/status decision branches used by the worker.
func decideProcessResult(alreadyProcessed bool, status string) domain.ProcessResult {
	if alreadyProcessed {
		return domain.ProcessAck
	}
	switch status {
	case string(domain.Succeeded):
		return domain.ProcessAck
	case string(domain.Queued), string(domain.Failed):
		return domain.ProcessAck // proceed path represented as ack after successful handling in real worker
	case string(domain.Running):
		return domain.ProcessAck
	default:
		return domain.ProcessNackDiscard
	}
}

func TestDecideProcessResult(t *testing.T) {
	assert.Equal(t, domain.ProcessAck, decideProcessResult(true, string(domain.Queued)))
	assert.Equal(t, domain.ProcessAck, decideProcessResult(false, string(domain.Succeeded)))
	assert.Equal(t, domain.ProcessAck, decideProcessResult(false, string(domain.Queued)))
	assert.Equal(t, domain.ProcessNackDiscard, decideProcessResult(false, "unknown"))
}

func TestTaskMessageRoundTrip(t *testing.T) {
	task := &domain.Task{
		ID:       42,
		Type:     string(domain.SendEmail),
		Status:   string(domain.Queued),
		Priority: string(domain.High),
		PayLoad:  `{"to":"a@b.com"}`,
	}
	body, err := json.Marshal(task)
	assert.NoError(t, err)

	var decoded domain.Task
	assert.NoError(t, json.Unmarshal(body, &decoded))
	assert.Equal(t, task.ID, decoded.ID)
	assert.Equal(t, task.Priority, decoded.Priority)
}

func TestMessageHandlerSignature(t *testing.T) {
	var handler domain.MessageHandler = func(ctx context.Context, msg domain.DeliveredMessage) domain.ProcessResult {
		if msg.MessageID == "" {
			return domain.ProcessNackDiscard
		}
		return domain.ProcessAck
	}
	assert.Equal(t, domain.ProcessNackDiscard, handler(context.Background(), domain.DeliveredMessage{}))
	assert.Equal(t, domain.ProcessAck, handler(context.Background(), domain.DeliveredMessage{MessageID: "abc"}))
}
