package domain

import "context"

// ProcessResult tells the consumer how to acknowledge a delivered message.
type ProcessResult int

const (
	ProcessAck ProcessResult = iota
	ProcessNackRequeue
	ProcessNackDiscard
)

// DeliveredMessage is a broker message delivered to a consumer.
type DeliveredMessage struct {
	MessageID    string
	Body         string
	DeliveryTag  uint64
	Redelivered  bool
	RetryCount   int64
	CorrelationID string
}

// MessageHandler processes a delivered message and returns how it should be acknowledged.
type MessageHandler func(ctx context.Context, msg DeliveredMessage) ProcessResult

// Queue abstracts message broker publish/consume operations.
type Queue interface {
	IsHealthy() bool
	PublishMessage(queueName, body, messageID string) error
	ConsumeMessages(consumerName, queueName string, handler MessageHandler) error
	Close() error
}

// OutboxEntry is a pending publish recorded transactionally with a domain write.
type OutboxEntry struct {
	ID        int64
	MessageID string
	QueueName string
	Payload   string
}
