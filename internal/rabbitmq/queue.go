package rabbitmq

import (
	"context"
	amqp "github.com/rabbitmq/amqp091-go"
	"log/slog"
)

type RabbitMQClient struct {
	ctx     context.Context
	conn    *amqp.Connection
	channel *amqp.Channel
}

var queueDeclarationHistory = map[string]bool{}

func NewRabbitMQClient(ctx context.Context, amqpURL string, mainQueueNames []string) (*RabbitMQClient, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		err2 := conn.Close()
		if err2 != nil {
			slog.Error("error occurred while closing connection", "error", err2.Error())
		}

		return nil, err
	}

	client := &RabbitMQClient{
		ctx:     ctx,
		conn:    conn,
		channel: ch,
	}
	err = client.checkMainQueueDeclarations(mainQueueNames)
	if err != nil {
		slog.Error("Error while checking declarations of main queues", "error", err.Error())
		return nil, err
	}

	return client, nil
}

func (c *RabbitMQClient) PublishMessage(queueName, body string) (err error) {
	err = c.checkQueueDeclaration(queueName)
	if err != nil {
		return err
	}

	return c.channel.PublishWithContext(
		c.ctx,
		"",        // exchange
		queueName, // routing key
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        []byte(body),
		})
}

func (c *RabbitMQClient) ConsumeMessages(consumerName, queueName string, handler func(string)) error {
	msgs, err := c.channel.ConsumeWithContext(
		c.ctx,
		queueName,    // queue
		consumerName, // consumer
		true,         // auto-ack
		false,        // exclusive
		false,        // no-local
		false,        // no-wait
		nil,          // args
	)

	if err != nil {
		return err
	}

	go func() {
		for d := range msgs {
			handler(string(d.Body))
		}
	}()

	return nil
}

func (c *RabbitMQClient) Close() error {
	err := c.channel.Close()
	if err != nil {
		return err
	}

	err = c.conn.Close()
	return err
}

func (c *RabbitMQClient) IsHealthy() bool {
	if c.conn.IsClosed() {
		slog.Error("RabbitMQ connection is closed, Rabbit is not healthy")
		return false
	}

	ch, err := c.conn.Channel()
	if err != nil {
		slog.Error("Failed to open RabbitMQ channel, Rabbit is not healthy", "error", err)
		return false
	}
	defer func() {
		err = ch.Close()
		if err != nil {
			slog.Error("Error occurred while closing rabbit channel created for health check", "error", err.Error())
		}
	}()

	return true
}

func (c *RabbitMQClient) checkMainQueueDeclarations(mainQueueNames []string) (err error) {
	for _, queueName := range mainQueueNames {
		err = c.checkQueueDeclaration(queueName)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *RabbitMQClient) checkQueueDeclaration(queueName string) (err error) {
	_, isDeclared := queueDeclarationHistory[queueName]
	if !isDeclared {
		_, err := c.channel.QueueDeclare(
			queueName, // name
			true,      // durable
			false,     // delete when unused
			false,     // exclusive
			false,     // no-wait
			nil,       // arguments
		)
		if err != nil {
			err2 := c.conn.Close()
			if err2 != nil {
				slog.Error("error occurred while closing connection", "error", err2.Error())
			}

			err2 = c.channel.Close()
			if err2 != nil {
				slog.Error("error occurred while closing channel", "error", err2.Error())
			}

			return err
		}

		queueDeclarationHistory[queueName] = true
	}

	return nil
}
