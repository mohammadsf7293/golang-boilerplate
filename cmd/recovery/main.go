package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"strconv"

	"github.com/mohammadsf7293/golang-boilerplate/configs"
	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
	"github.com/mohammadsf7293/golang-boilerplate/internal/errval"
	"github.com/mohammadsf7293/golang-boilerplate/internal/outbox"
	"github.com/mohammadsf7293/golang-boilerplate/internal/postgres"
	"github.com/mohammadsf7293/golang-boilerplate/internal/rabbitmq"
)

func main() {
	cfg := configs.InitConfig()
	args := os.Args
	if len(args) < 4 {
		log.Fatal("Insufficient arguments are provided in calling the command. Usage: queue_recovery <taskStatus|running> <pastSeconds> <limit>")
		return
	}

	taskStatus := args[1]
	if taskStatus != string(domain.Queued) && taskStatus != string(domain.Failed) && taskStatus != string(domain.Running) {
		slog.Error("only queued, failed, or running tasks can be re-queued", "provided_task_status", taskStatus)
		return
	}

	pastSecondsStr := args[2]
	pastSeconds, err := strconv.ParseInt(pastSecondsStr, 10, 64)
	if err != nil {
		log.Fatal("Invalid input is given for the pastSeconds arg, it must be an integer", "provided_past_seconds", pastSecondsStr, "error", err)
		return
	}

	limitStr := args[3]
	limit, err := strconv.ParseInt(limitStr, 10, 64)
	if err != nil {
		log.Fatal("Invalid input is given for the limit arg, it must be an integer", "provided_limit", limitStr, "error", err)
		return
	}

	ctx := context.Background()
	storage, err := postgres.NewStorage(ctx, cfg.Database.ToDbConnectionUri())
	if err != nil {
		log.Fatal(err)
	}
	slog.Info("Postgres connection has been initialized successfully")

	mainQueueNames := cfg.RabbitMQ.GetMainQueueNames()
	rabbitClient, err := rabbitmq.NewRabbitMQClient(ctx, cfg.RabbitMQ.ToRabbitConnectionUri(), mainQueueNames, rabbitmq.ClientOptions{
		PrefetchCount: cfg.RabbitPrefetchCount,
		DLXName:       cfg.RabbitMQ.DeadLetterExchange,
		DLQName:       cfg.RabbitMQ.DeadLetterQueueName,
		MaxRetries:    cfg.RabbitMaxRetries,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := rabbitClient.Close(); err != nil {
			slog.Error("An error occurred while closing RabbitMQ connection", "error", err.Error())
		}
	}()
	slog.Info("RabbitMQ has been initialized successfully")

	var tasks []*domain.Task
	if taskStatus == string(domain.Running) {
		slog.Info("Reclaiming stale running tasks", "past_seconds_threshold", pastSeconds, "limit", limit)
		tasks, err = storage.ReclaimStaleRunningTasks(ctx, int32(pastSeconds), int32(limit))
		if err != nil {
			if err == errval.ErrNotFound {
				slog.Info("No stale running tasks found")
				return
			}
			slog.Error("Error occurred while reclaiming stale running tasks", "error", err.Error())
			return
		}
	} else {
		slog.Info("Fetching missed tasks", "task_status", taskStatus, "past_seconds_threshold", pastSeconds, "limit", limit)
		tasks, err = storage.GetMissedTasks(ctx, taskStatus, int32(pastSeconds), int32(limit))
		if err != nil {
			if err == errval.ErrNotFound {
				slog.Info("No missed tasks found")
				return
			}
			slog.Error("Error occurred while fetching missed tasks", "error", err.Error())
			return
		}
	}
	slog.Info("Tasks selected for re-queue via outbox", "count", len(tasks), "task_status", taskStatus)

	requeuedCount := 0
	for i, task := range tasks {
		queueName := cfg.RabbitMQ.NormalPriorityJobsQueueName
		switch task.Priority {
		case string(domain.High):
			queueName = cfg.RabbitMQ.HighPriorityJobsQueueName
		case string(domain.Low):
			queueName = cfg.RabbitMQ.LowPriorityJobsQueueName
		}

		messageID, err := storage.EnqueueTaskToOutbox(ctx, task, queueName)
		if err != nil {
			slog.Error("Error occurred while writing task to outbox", "task_id", task.ID, "error", err.Error(), "item_index", i)
			continue
		}
		slog.Info("Task written to outbox for re-queue", "task_id", task.ID, "message_id", messageID, "priority", task.Priority)
		requeuedCount++
	}

	// Flush outbox once so recovery does not wait for the server relay.
	relay := outbox.NewRelay(storage, rabbitClient, int32(limit), 0)
	relay.PublishOnce(ctx)

	slog.Info("Missed tasks have been re-queued via outbox", "selected_count", len(tasks), "successful_outbox_count", requeuedCount)
}
