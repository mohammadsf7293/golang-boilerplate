package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cenkalti/backoff/v4"
	"github.com/gin-gonic/gin"
	"github.com/sf7293/task-manager/configs"
	"github.com/sf7293/task-manager/internal/domain"
	"github.com/sf7293/task-manager/internal/postgres"
	"github.com/sf7293/task-manager/internal/rabbitmq"
	"github.com/sf7293/task-manager/internal/redis"
	process2 "github.com/sf7293/task-manager/pkg/process"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var postgresIsReady, rabbitIsReady, redisIsReady bool

func main() {
	cfg := configs.InitConfig()
	args := os.Args
	slog.Info("Running job_worker command", "args", args, "len_args", len(args))
	if len(args) < 2 {
		log.Fatal("Insufficient arguments are provided in calling the command")
		return
	}

	// workerPriority is an enum ('high','normal','low')
	// workerNumber is an index showing the id of the worker (It's only needed to be unique, and there is no requirement of being a number)
	var workerPriority, workerNumber string
	// In the Kubernetes helm, it passes firstArg and secondArgs as one string arg: "{firstArg} {secondArg}"
	if strings.Contains(os.Args[1], " ") {
		splitArgs := strings.Split(args[1], " ")
		if len(splitArgs) < 2 {
			log.Fatal("Insufficient args detected when splitting the first arg", "splitted_args", splitArgs)
			return
		}
		workerPriority = splitArgs[0]
		workerNumber = splitArgs[1]
	} else {
		workerPriority = args[1]
		workerNumber = args[2]
	}

	if workerPriority != string(domain.High) && workerPriority != string(domain.Normal) && workerPriority != string(domain.Low) {
		log.Fatal("Invalid argument is set for priority, it can only be high, normal, or low")
		return
	}

	// Setting up a context with cfg.WorkerTimeOutInSeconds seconds time out, which limits the task process time with a timeout of cfg.WorkerTimeOutInSeconds seconds
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.WorkerTimeOutInSeconds)*time.Second)
	defer cancel()

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
	rabbitIsReady = true
	slog.Info("RabbitMQ connection has been initialized successfully")

	redisClient, err := redis.NewClient(ctx, cfg.RedisConfig.ToRedisConnectionUri())
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err = redisClient.Close()
		if err != nil {
			slog.Error("An error occurred while closing Redis connection", "error", err.Error())
		}
	}()
	redisIsReady = true
	slog.Info("Redis connection has been initialized successfully")

	storage, err := postgres.NewStorage(ctx, cfg.Database.ToDbConnectionUri())
	if err != nil {
		log.Fatal(err)
	}
	postgresIsReady = true
	slog.Info("Postgres connection has been initialized successfully")

	handlerFunc := func(input string) {
		task := new(domain.Task)
		err := json.Unmarshal([]byte(input), &task)
		if err != nil {
			slog.Error("There was an error in unmarshalling the item", "error", err)
			return
		}
		slog.Info("Task is picked up from the queue", "task_id", task.ID)

		if task.Status != string(domain.Queued) && task.Status != string(domain.Failed) {
			slog.Error("Task with invalid status has been pushed to queue, ignoring the task...", "task_id", task.ID, "task_status", task.Status)
			return
		}

		// Handling concurrency problems using distributed lock system => A task cannot be processed simultaneously via two workers
		lockKey := "lock:" + strconv.FormatInt(int64(task.ID), 10)
		slog.Info("Locking the key in distributed lock system", "lock_key", lockKey)
		isLocked, err := redisClient.Lock(lockKey, time.Duration(10)*time.Second)
		if err != nil {
			slog.Error("Error occurred while locking the key for task", "lock_key", lockKey, "error", err.Error())
			return
		}
		if !isLocked {
			slog.Error("Concurrent processing error happened for the task, ignoring running current process...", "task_id", task.ID)
			return
		}
		slog.Info("Key is locked successfully for the task in the distributed lock infra", "lock_key", lockKey)
		defer func() {
			err = redisClient.Unlock(lockKey)
			if err != nil {
				slog.Error("Error while unlocking locked key", "lock_key", lockKey, "err", err.Error())
			}
		}()

		// First unmarshal to get the inner JSON string
		var innerJSONString string
		err = json.Unmarshal([]byte(task.PayLoad), &innerJSONString)
		if err != nil {
			slog.Error("Error occurred while unmarshalling inner JSON string:", "error", err.Error(), "task_id", task.ID, "payload", task.PayLoad)
			slog.Info(fmt.Sprintf("Updating task state from from '%s' to 'failed'", task.Status), "task_id", task.ID)
			err = storage.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, task.Status, string(domain.Failed))
			if err != nil {
				slog.Error("There was an error in updating task status to failed", "error", err, "task_id", task.ID)
				return
			}
			slog.Info(fmt.Sprintf("Task state is changed from '%s' to 'failed'", task.Status), "task_id", task.ID)
			return
		}

		paramsMap := map[string]string{}
		err = json.Unmarshal([]byte(innerJSONString), &paramsMap)
		if err != nil {
			slog.Error("Failed to unmarshal innerJSONString to params map[string]string", "error", err, "task_id", task.ID, "inner_json_str", innerJSONString, "error", err.Error())
			slog.Info(fmt.Sprintf("Updating task state from from '%s' to 'failed'", task.Status), "task_id", task.ID)
			err = storage.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, task.Status, string(domain.Failed))
			if err != nil {
				slog.Error("There was an error in updating task status to failed", "error", err, "task_id", task.ID)
				return
			}
			slog.Info(fmt.Sprintf("Task state is changed from '%s' to 'failed'", task.Status), "task_id", task.ID)
			return
		}
		slog.Info("Payload field of the task, is marshalled into a map[string]string", "task_id", task.ID)

		process, err := process2.NewProcess(domain.TaskType(task.Type))
		if err != nil {
			slog.Error("Error while creating process for the task", "task_id", task.ID, "task_type", task.Type, "error", err.Error())
			slog.Info(fmt.Sprintf("Updating task state from from '%s' to 'failed'", task.Status), "task_id", task.ID)
			// Updating task status to failed
			err = storage.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, task.Status, string(domain.Failed))
			if err != nil {
				slog.Error("There was an error in updating task status to failed", "error", err, "task_id", task.ID)
				return
			}
			slog.Info(fmt.Sprintf("Task state is changed from '%s' to 'failed'", task.Status), "task_id", task.ID)
			return
		}

		// Atomic changing task status, and insertion of the log in the tasks_status_change_history table
		slog.Info(fmt.Sprintf("Updating task state from from '%s' to 'running'", task.Status), "task_id", task.ID)
		err = storage.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, task.Status, string(domain.Running))
		if err != nil {
			slog.Error("There was an error in updating task status to running", "error", err, "task_id", task.ID)
			return
		}
		slog.Info(fmt.Sprintf("Task state is changed from '%s' to 'running'", task.Status), "task_id", task.ID)

		operation := func() error {
			return process.Execute(paramsMap)
		}

		// Implementation of retrial of the operation, in case of failure
		err = backoff.Retry(operation, backoff.NewExponentialBackOff())
		if err != nil {
			slog.Error("Error has happened while doing the task", "task_id", task.ID, "task_type", task.Type, "params", paramsMap)

			// Updating task status to failed
			err = storage.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, string(domain.Running), string(domain.Failed))
			if err != nil {
				slog.Error("There was an error in updating task status to failed", "error", err, "task_id", task.ID)
				return
			}

			slog.Info("Task state is changed from 'running' to 'failed'", "task_id", task.ID)
			return
		}

		// Updating task status to succeeded
		slog.Info("Updating task state from 'running' to 'succeeded'", "task_id", task.ID)
		err = storage.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, string(domain.Running), string(domain.Succeeded))
		if err != nil {
			slog.Error("There was an error in updating task status to succeeded", "error", err, "task_id", task.ID)
			return
		}
		slog.Info("Task state is changed from 'running' to 'succeeded'", "task_id", task.ID)

		slog.Info("Task running has been successfully finished", "task_id", task.ID, "task_type", task.Type)
		return
	}

	// Channel to listen for interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	queueName := cfg.RabbitMQ.NormalPriorityJobsQueueName
	switch workerPriority {
	case string(domain.High):
		queueName = cfg.RabbitMQ.HighPriorityJobsQueueName
	case string(domain.Low):
		queueName = cfg.RabbitMQ.LowPriorityJobsQueueName
	}

	consumerName := "my-consumer:" + workerNumber
	slog.Info("Creating consumer for RabbitMQ", "queueName", queueName, "consumer_name", consumerName)
	// The consumer name must be unique for each worker, so I've added workerNumber to it
	err = rabbitClient.ConsumeMessages(consumerName, queueName, handlerFunc)
	if err != nil {
		log.Fatalf("Failed to start consuming messages: %v", err)
	}
	slog.Info("Consumer is created successfully", "queueName", queueName, "consumer_name", consumerName)

	// Running HTTP Server in order to have liveness and readiness HTTP APIs
	go setUpHealthCheckerAPIs(ctx, cfg, storage, rabbitClient, redisClient)

	slog.Info("Worker is running. To exit press CTRL+C", "worker_num", workerNumber)
	<-sigChan // Wait for interrupt signal
	slog.Info("Worker is shutting down...", "worker_num", workerNumber)
}

func setUpHealthCheckerAPIs(ctx context.Context, cfg *configs.Config, storage domain.Storage, rabbitClient *rabbitmq.RabbitMQClient, redisClient *redis.Client) {
	r := gin.Default()
	r.GET("/readiness", func(c *gin.Context) {
		if postgresIsReady && rabbitIsReady && redisIsReady {
			c.JSON(http.StatusOK, gin.H{"status": "ready"})
		} else {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
		}

		c.JSON(http.StatusOK, gin.H{"status": "up"})
	})
	r.GET("/liveness", func(c *gin.Context) {
		err := storage.Ping(ctx)
		if err != nil {
			slog.Error("Postgresql seem not to be pingable in liveness API", "error", err.Error())
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not healthy"})
			return
		}

		isRabbitHealthy := rabbitClient.IsHealthy()
		if !isRabbitHealthy {
			slog.Error("Rabbit is not healthy")
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not healthy"})
			return
		}

		err = redisClient.Ping(ctx)
		if err != nil {
			slog.Error("Redis seem not to be pingable in liveness API", "error", err.Error())
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not healthy"})
			return
		}
	})

	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: r,
	}

	// Initializing the server in a goroutine so that
	// it won't block the graceful shutdown handling below
	go func() {
		log.Printf("Starting server on port %s\n", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")
}
