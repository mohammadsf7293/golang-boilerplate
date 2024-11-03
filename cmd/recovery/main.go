package main

import (
	"context"
	"encoding/json"
	"github.com/sf7293/task-manager/configs"
	"github.com/sf7293/task-manager/internal/domain"
	"github.com/sf7293/task-manager/internal/postgres"
	"github.com/sf7293/task-manager/internal/rabbitmq"
	"log"
	"log/slog"
	"os"
	"strconv"
)

func main() {
	cfg := configs.InitConfig()
	args := os.Args
	if len(args) < 4 {
		log.Fatal("Insufficient arguments are provided in calling the command")
		return
	}

	taskStatus := args[1]
	if taskStatus != string(domain.Queued) && taskStatus != string(domain.Failed) {
		slog.Error("only queued and failed tasks can be re-queued", "provided_task_status", taskStatus)
		return
	}

	// This argument defines the condition for the query, The query finds tasks with the given 'taskStatus' whose updated_at has not been changed since passed X seconds (their updated_at <= nowStamp - passedXSeconds)
	pastSecondsStr := args[2]
	pastSeconds, err := strconv.ParseInt(pastSecondsStr, 10, 64)
	if err != nil {
		log.Fatal("Invalid input is given for the pastSeconds arg, it must be an integer", "provided_past_seconds", pastSecondsStr, "error", err)
		return
	}

	// This argument defines maximum number of tasks to be fetched by query
	limitStr := args[2]
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
	rabbitClient, err := rabbitmq.NewRabbitMQClient(ctx, cfg.RabbitMQ.ToRabbitConnectionUri(), mainQueueNames)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err = rabbitClient.Close()
		if err != nil {
			slog.Error("An error occurred while closing RabbitMQ connection", "error", err.Error())
		}
	}()
	slog.Info("RabbitMQ has been initialized successfully")

	slog.Info("Fetching missed tasks", "task_status", taskStatus, "past_seconds_threshold", pastSeconds, "limit", limit)
	missedTasks, err := storage.GetMissedTasks(ctx, taskStatus, int32(pastSeconds), int32(limit))
	if err != nil {
		slog.Error("Error occurred while fetching missed tasks", "error", err.Error())
		return
	}
	slog.Info("Missed tasks are fetched", "task_status", taskStatus, "past_seconds_threshold", pastSeconds, "limit", limit, "fetched_items_count", len(missedTasks))

	requeuedCount := 0
	for i, task := range missedTasks {
		jobsQueueName := cfg.RabbitMQ.NormalPriorityJobsQueueName
		switch task.Priority {
		case string(domain.High):
			jobsQueueName = cfg.RabbitMQ.HighPriorityJobsQueueName
		case string(domain.Low):
			jobsQueueName = cfg.RabbitMQ.LowPriorityJobsQueueName
		}

		slog.Info("Start of marshalling task", "task_id", task.ID, "missed_tasks_count", len(missedTasks), "item_index", i)
		marshalledTask, err := json.Marshal(task)
		if err != nil {
			slog.Error("There was an error in marshalling task", "task_id", task.ID, "error", err.Error())
			// I've ignored returning the error here and just log it because I'll handle re-queueing task again in another worker
			continue
		}
		slog.Info("Task is marshalled successfully and ready to be re-queued", "task_id", task.ID)

		err = rabbitClient.PublishMessage(jobsQueueName, string(marshalledTask))
		if err != nil {
			slog.Error("Error occurred while queuing marshalled task to jobs queue", "error", err.Error())
			// Again I've ignored returning the error here and just log it because I'll handle re-queueing task again in another worker
		}
		slog.Info("Task is re-queued successfully", "task_id", task.ID, "priority", task.Priority, "missed_tasks_count", len(missedTasks), "item_index", i)
		requeuedCount++
	}

	slog.Info("Missed tasks have been re-queued", "missed_tasks_count", len(missedTasks), "successful_requeued_count", requeuedCount)
}
