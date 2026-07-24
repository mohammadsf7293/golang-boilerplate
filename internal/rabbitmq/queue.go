package rabbitmq

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
)

type RabbitMQClient struct {
	ctx            context.Context
	amqpURL        string
	mainQueueNames []string
	prefetchCount  int
	dlxName        string
	dlqName        string
	maxRetries     int64

	mu      sync.RWMutex
	conn    *amqp.Connection
	channel *amqp.Channel

	queueDeclarationHistory map[string]bool
}

type ClientOptions struct {
	PrefetchCount int
	DLXName       string
	DLQName       string
	MaxRetries    int64
}

func NewRabbitMQClient(ctx context.Context, amqpURL string, mainQueueNames []string, opts ClientOptions) (*RabbitMQClient, error) {
	if opts.PrefetchCount <= 0 {
		opts.PrefetchCount = 10
	}
	if opts.DLXName == "" {
		opts.DLXName = "jobs.dlx"
	}
	if opts.DLQName == "" {
		opts.DLQName = "jobs.dlq"
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 5
	}

	client := &RabbitMQClient{
		ctx:                     ctx,
		amqpURL:                 amqpURL,
		mainQueueNames:          mainQueueNames,
		prefetchCount:           opts.PrefetchCount,
		dlxName:                 opts.DLXName,
		dlqName:                 opts.DLQName,
		maxRetries:              opts.MaxRetries,
		queueDeclarationHistory: map[string]bool{},
	}

	if err := client.connect(); err != nil {
		return nil, err
	}

	return client, nil
}

func (c *RabbitMQClient) connect() error {
	var conn *amqp.Connection
	var ch *amqp.Channel

	err := backoff.Retry(func() error {
		var dialErr error
		conn, dialErr = amqp.Dial(c.amqpURL)
		if dialErr != nil {
			slog.Error("failed to dial RabbitMQ.. retrying...", "error", dialErr)
			return dialErr
		}

		ch, dialErr = conn.Channel()
		if dialErr != nil {
			_ = conn.Close()
			slog.Error("failed to open RabbitMQ channel.. retrying...", "error", dialErr)
			return dialErr
		}
		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(2*time.Second), 5))
	if err != nil {
		return err
	}

	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("enable publisher confirms: %w", err)
	}

	if err := ch.Qos(c.prefetchCount, 0, false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("set qos: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.channel = ch
	c.queueDeclarationHistory = map[string]bool{}
	c.mu.Unlock()

	if err := c.declareDeadLetterInfrastructure(); err != nil {
		return err
	}

	if err := c.checkMainQueueDeclarations(c.mainQueueNames); err != nil {
		return err
	}

	return nil
}

func (c *RabbitMQClient) declareDeadLetterInfrastructure() error {
	c.mu.RLock()
	ch := c.channel
	c.mu.RUnlock()

	if err := ch.ExchangeDeclare(c.dlxName, "direct", true, false, false, false, nil); err != nil {
		return err
	}

	_, err := ch.QueueDeclare(c.dlqName, true, false, false, false, nil)
	if err != nil {
		return err
	}

	return ch.QueueBind(c.dlqName, c.dlqName, c.dlxName, false, nil)
}

func (c *RabbitMQClient) PublishMessage(queueName, body, messageID string) error {
	if err := c.ensureChannel(); err != nil {
		return err
	}
	if err := c.checkQueueDeclaration(queueName); err != nil {
		return err
	}

	c.mu.RLock()
	ch := c.channel
	c.mu.RUnlock()

	confirmation, err := ch.PublishWithDeferredConfirmWithContext(
		c.ctx,
		"",
		queueName,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    messageID,
			Timestamp:    time.Now().UTC(),
			Body:         []byte(body),
			Headers: amqp.Table{
				"x-retry-count": int64(0),
			},
		},
	)
	if err != nil {
		return err
	}
	if confirmation == nil {
		return fmt.Errorf("publisher confirms not enabled")
	}
	acked, err := confirmation.WaitContext(c.ctx)
	if err != nil {
		return err
	}
	if !acked {
		return fmt.Errorf("publish not confirmed by broker for message_id=%s", messageID)
	}
	return nil
}

func (c *RabbitMQClient) ConsumeMessages(consumerName, queueName string, handler domain.MessageHandler) error {
	if err := c.ensureChannel(); err != nil {
		return err
	}
	if err := c.checkQueueDeclaration(queueName); err != nil {
		return err
	}

	c.mu.RLock()
	ch := c.channel
	c.mu.RUnlock()

	msgs, err := ch.ConsumeWithContext(
		c.ctx,
		queueName,
		consumerName,
		false, // manual ack
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	go func() {
		for d := range msgs {
			retryCount := extractRetryCount(d)
			msg := domain.DeliveredMessage{
				MessageID:     d.MessageId,
				Body:          string(d.Body),
				DeliveryTag:   d.DeliveryTag,
				Redelivered:   d.Redelivered,
				RetryCount:    retryCount,
				CorrelationID: d.CorrelationId,
			}

			result := handler(c.ctx, msg)
			switch result {
			case domain.ProcessAck:
				if err := d.Ack(false); err != nil {
					slog.Error("failed to ack message", "error", err, "message_id", msg.MessageID)
				}
			case domain.ProcessNackRequeue:
				if retryCount >= c.maxRetries {
					if err := c.publishToDLQ(d, retryCount); err != nil {
						slog.Error("failed to publish to DLQ", "error", err, "message_id", msg.MessageID)
					}
					if err := d.Ack(false); err != nil {
						slog.Error("failed to ack after DLQ publish", "error", err, "message_id", msg.MessageID)
					}
					continue
				}
				if err := c.republishWithRetry(d, retryCount+1); err != nil {
					slog.Error("failed to republish with retry", "error", err, "message_id", msg.MessageID)
					_ = d.Nack(false, true)
					continue
				}
				if err := d.Ack(false); err != nil {
					slog.Error("failed to ack after republish", "error", err, "message_id", msg.MessageID)
				}
			case domain.ProcessNackDiscard:
				if err := c.publishToDLQ(d, retryCount); err != nil {
					slog.Error("failed to publish to DLQ", "error", err, "message_id", msg.MessageID)
					_ = d.Nack(false, false)
					continue
				}
				if err := d.Ack(false); err != nil {
					slog.Error("failed to ack after DLQ publish", "error", err, "message_id", msg.MessageID)
				}
			}
		}
	}()

	return nil
}

func (c *RabbitMQClient) republishWithRetry(d amqp.Delivery, retryCount int64) error {
	c.mu.RLock()
	ch := c.channel
	c.mu.RUnlock()

	headers := amqp.Table{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	headers["x-retry-count"] = retryCount

	confirmation, err := ch.PublishWithDeferredConfirmWithContext(
		c.ctx,
		"",
		d.RoutingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:   d.ContentType,
			DeliveryMode:  amqp.Persistent,
			MessageId:     d.MessageId,
			CorrelationId: d.CorrelationId,
			Timestamp:     time.Now().UTC(),
			Body:          d.Body,
			Headers:       headers,
		},
	)
	if err != nil {
		return err
	}
	acked, err := confirmation.WaitContext(c.ctx)
	if err != nil {
		return err
	}
	if !acked {
		return fmt.Errorf("retry republish not confirmed")
	}
	return nil
}

func (c *RabbitMQClient) publishToDLQ(d amqp.Delivery, retryCount int64) error {
	c.mu.RLock()
	ch := c.channel
	c.mu.RUnlock()

	headers := amqp.Table{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	headers["x-retry-count"] = retryCount
	headers["x-original-routing-key"] = d.RoutingKey

	confirmation, err := ch.PublishWithDeferredConfirmWithContext(
		c.ctx,
		c.dlxName,
		c.dlqName,
		false,
		false,
		amqp.Publishing{
			ContentType:   d.ContentType,
			DeliveryMode:  amqp.Persistent,
			MessageId:     d.MessageId,
			CorrelationId: d.CorrelationId,
			Timestamp:     time.Now().UTC(),
			Body:          d.Body,
			Headers:       headers,
		},
	)
	if err != nil {
		return err
	}
	acked, err := confirmation.WaitContext(c.ctx)
	if err != nil {
		return err
	}
	if !acked {
		return fmt.Errorf("dlq publish not confirmed")
	}
	return nil
}

func extractRetryCount(d amqp.Delivery) int64 {
	if d.Headers == nil {
		return 0
	}
	raw, ok := d.Headers["x-retry-count"]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case int64:
		return v
	case int32:
		return int64(v)
	case int:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	default:
		return 0
	}
}

func (c *RabbitMQClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var firstErr error
	if c.channel != nil {
		if err := c.channel.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *RabbitMQClient) IsHealthy() bool {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil || conn.IsClosed() {
		slog.Error("RabbitMQ connection is closed, Rabbit is not healthy")
		return false
	}

	ch, err := conn.Channel()
	if err != nil {
		slog.Error("Failed to open RabbitMQ channel, Rabbit is not healthy", "error", err)
		return false
	}
	defer func() {
		if err := ch.Close(); err != nil {
			slog.Error("Error occurred while closing rabbit channel created for health check", "error", err.Error())
		}
	}()

	return true
}

func (c *RabbitMQClient) ensureChannel() error {
	c.mu.RLock()
	conn := c.conn
	ch := c.channel
	c.mu.RUnlock()

	if conn != nil && !conn.IsClosed() && ch != nil && !ch.IsClosed() {
		return nil
	}

	slog.Warn("RabbitMQ connection unhealthy, reconnecting...")
	return c.connect()
}

func (c *RabbitMQClient) checkMainQueueDeclarations(mainQueueNames []string) error {
	seen := map[string]struct{}{}
	for _, queueName := range mainQueueNames {
		if _, ok := seen[queueName]; ok {
			continue
		}
		seen[queueName] = struct{}{}
		if err := c.checkQueueDeclaration(queueName); err != nil {
			return err
		}
	}
	return nil
}

func (c *RabbitMQClient) checkQueueDeclaration(queueName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.queueDeclarationHistory[queueName] {
		return nil
	}

	args := amqp.Table{
		"x-dead-letter-exchange":    c.dlxName,
		"x-dead-letter-routing-key": c.dlqName,
	}

	_, err := c.channel.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		args,
	)
	if err != nil {
		return err
	}

	c.queueDeclarationHistory[queueName] = true
	return nil
}
