package domain

type Queue interface {
	IsHealthy() bool
	PublishMessage(queueName, body string) error
	ConsumeMessages(consumerName, queueName string, handler func(string)) error
	Close() error
}
