package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/mohammadsf7293/golang-boilerplate/configs"
	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
	"github.com/mohammadsf7293/golang-boilerplate/internal/errval"
	"github.com/mohammadsf7293/golang-boilerplate/internal/metrics"
	"github.com/mohammadsf7293/golang-boilerplate/internal/postgres"
	"github.com/mohammadsf7293/golang-boilerplate/internal/rabbitmq"
	"github.com/mohammadsf7293/golang-boilerplate/internal/redis"
	process2 "github.com/mohammadsf7293/golang-boilerplate/pkg/process"
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

	var workerPriority, workerNumber string
	if strings.Contains(os.Args[1], " ") {
		splitArgs := strings.Split(args[1], " ")
		if len(splitArgs) < 2 {
			log.Fatal("Insufficient args detected when splitting the first arg", "splitted_args", splitArgs)
			return
		}
		workerPriority = splitArgs[0]
		workerNumber = splitArgs[1]
	} else {
		if len(args) < 3 {
			log.Fatal("Insufficient arguments are provided in calling the command")
			return
		}
		workerPriority = args[1]
		workerNumber = args[2]
	}

	if workerPriority != string(domain.High) && workerPriority != string(domain.Normal) && workerPriority != string(domain.Low) {
		log.Fatal("Invalid argument is set for priority, it can only be high, normal, or low")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	rabbitIsReady = true
	slog.Info("RabbitMQ connection has been initialized successfully")

	var distributedLock domain.DistributedLock
	redisClient, err := redis.NewClient(ctx, cfg.RedisConfig.ToRedisConnectionUri())
	if err != nil {
		log.Fatal(err)
	}
	distributedLock = redisClient
	defer func() {
		if err := distributedLock.Close(); err != nil {
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

	lockTTL := time.Duration(cfg.WorkerLockTTLInSeconds) * time.Second
	workTimeout := time.Duration(cfg.WorkerTimeOutInSeconds) * time.Second

	handlerFunc := func(_ context.Context, msg domain.DeliveredMessage) domain.ProcessResult {
		start := time.Now()
		defer func() {
			metrics.TaskProcessingDuration.Observe(time.Since(start).Seconds())
		}()

		if msg.MessageID == "" {
			slog.Error("message missing message_id, discarding")
			metrics.TasksProcessedTotal.WithLabelValues("discarded").Inc()
			return domain.ProcessNackDiscard
		}

		processed, err := storage.IsMessageProcessed(ctx, msg.MessageID)
		if err != nil {
			slog.Error("failed checking inbox", "error", err, "message_id", msg.MessageID)
			return domain.ProcessNackRequeue
		}
		if processed {
			slog.Info("duplicate message skipped via inbox", "message_id", msg.MessageID)
			metrics.InboxDuplicatesTotal.Inc()
			metrics.TasksProcessedTotal.WithLabelValues("duplicate").Inc()
			return domain.ProcessAck
		}

		task := new(domain.Task)
		if err := json.Unmarshal([]byte(msg.Body), &task); err != nil {
			slog.Error("There was an error in unmarshalling the item", "error", err, "message_id", msg.MessageID)
			metrics.TasksProcessedTotal.WithLabelValues("invalid_payload").Inc()
			return domain.ProcessNackDiscard
		}
		slog.Info("Task is picked up from the queue", "task_id", task.ID, "message_id", msg.MessageID)

		msgCtx, msgCancel := context.WithTimeout(ctx, workTimeout)
		defer msgCancel()

		dbTask, err := storage.GetTaskByID(msgCtx, task.ID)
		if err != nil {
			if err == errval.ErrNotFound {
				slog.Error("task not found in database", "task_id", task.ID)
				return domain.ProcessNackDiscard
			}
			slog.Error("failed loading task from database", "error", err, "task_id", task.ID)
			return domain.ProcessNackRequeue
		}

		if dbTask.Status == string(domain.Succeeded) {
			if err := storage.RecordInboxMessage(msgCtx, msg.MessageID, dbTask.ID); err != nil {
				slog.Error("failed recording inbox for already-succeeded task", "error", err, "task_id", dbTask.ID)
				return domain.ProcessNackRequeue
			}
			slog.Info("task already succeeded, acknowledging", "task_id", dbTask.ID)
			metrics.TasksProcessedTotal.WithLabelValues("already_succeeded").Inc()
			return domain.ProcessAck
		}
		if dbTask.Status != string(domain.Queued) && dbTask.Status != string(domain.Failed) {
			slog.Error("Task with invalid status has been pushed to queue, ignoring", "task_id", dbTask.ID, "task_status", dbTask.Status)
			metrics.TasksProcessedTotal.WithLabelValues("invalid_status").Inc()
			return domain.ProcessAck
		}

		lockKey := "lock:" + strconv.FormatInt(int64(dbTask.ID), 10)
		isLocked, err := distributedLock.Lock(lockKey, lockTTL)
		if err != nil {
			slog.Error("Error occurred while locking the key for task", "lock_key", lockKey, "error", err.Error())
			return domain.ProcessNackRequeue
		}
		if !isLocked {
			slog.Error("Concurrent processing error happened for the task", "task_id", dbTask.ID)
			metrics.LockContentionTotal.Inc()
			return domain.ProcessNackRequeue
		}
		defer func() {
			if err := distributedLock.Unlock(lockKey); err != nil {
				slog.Error("Error while unlocking locked key", "lock_key", lockKey, "err", err.Error())
			}
		}()

		var innerJSONString string
		if err := json.Unmarshal([]byte(dbTask.PayLoad), &innerJSONString); err != nil {
			slog.Error("Error occurred while unmarshalling inner JSON string", "error", err.Error(), "task_id", dbTask.ID)
			if err := storage.UpdateTaskStatusWithInboxInTx(msgCtx, dbTask.ID, dbTask.Status, string(domain.Failed), msg.MessageID); err != nil {
				slog.Error("failed marking task failed", "error", err, "task_id", dbTask.ID)
				return domain.ProcessNackRequeue
			}
			metrics.TasksProcessedTotal.WithLabelValues("failed").Inc()
			return domain.ProcessAck
		}

		paramsMap := map[string]string{}
		if err := json.Unmarshal([]byte(innerJSONString), &paramsMap); err != nil {
			slog.Error("Failed to unmarshal payload params", "error", err, "task_id", dbTask.ID)
			if err := storage.UpdateTaskStatusWithInboxInTx(msgCtx, dbTask.ID, dbTask.Status, string(domain.Failed), msg.MessageID); err != nil {
				slog.Error("failed marking task failed", "error", err, "task_id", dbTask.ID)
				return domain.ProcessNackRequeue
			}
			metrics.TasksProcessedTotal.WithLabelValues("failed").Inc()
			return domain.ProcessAck
		}

		process, err := process2.NewProcess(domain.TaskType(dbTask.Type))
		if err != nil {
			slog.Error("Error while creating process for the task", "task_id", dbTask.ID, "error", err.Error())
			if err := storage.UpdateTaskStatusWithInboxInTx(msgCtx, dbTask.ID, dbTask.Status, string(domain.Failed), msg.MessageID); err != nil {
				slog.Error("failed marking task failed", "error", err, "task_id", dbTask.ID)
				return domain.ProcessNackRequeue
			}
			metrics.TasksProcessedTotal.WithLabelValues("failed").Inc()
			return domain.ProcessAck
		}

		if err := storage.UpdateTaskStatusAndLogChangeInTx(msgCtx, dbTask.ID, dbTask.Status, string(domain.Running)); err != nil {
			if err == errval.ErrConcurrentUpdate {
				slog.Info("lost CAS race moving task to running", "task_id", dbTask.ID)
				return domain.ProcessAck
			}
			slog.Error("There was an error in updating task status to running", "error", err, "task_id", dbTask.ID)
			return domain.ProcessNackRequeue
		}

		operation := func() error {
			return process.Execute(paramsMap)
		}
		bo := backoff.NewExponentialBackOff()
		bo.MaxElapsedTime = workTimeout
		err = backoff.Retry(operation, backoff.WithContext(bo, msgCtx))
		if err != nil {
			slog.Error("Error has happened while doing the task", "task_id", dbTask.ID, "error", err)
			if updateErr := storage.UpdateTaskStatusWithInboxInTx(msgCtx, dbTask.ID, string(domain.Running), string(domain.Failed), msg.MessageID); updateErr != nil {
				slog.Error("There was an error in updating task status to failed", "error", updateErr, "task_id", dbTask.ID)
				return domain.ProcessNackRequeue
			}
			metrics.TasksProcessedTotal.WithLabelValues("failed").Inc()
			return domain.ProcessAck
		}

		if err := storage.UpdateTaskStatusWithInboxInTx(msgCtx, dbTask.ID, string(domain.Running), string(domain.Succeeded), msg.MessageID); err != nil {
			slog.Error("There was an error in updating task status to succeeded", "error", err, "task_id", dbTask.ID)
			return domain.ProcessNackRequeue
		}

		slog.Info("Task running has been successfully finished", "task_id", dbTask.ID, "task_type", dbTask.Type)
		metrics.TasksProcessedTotal.WithLabelValues("succeeded").Inc()
		return domain.ProcessAck
	}

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
	if err := rabbitClient.ConsumeMessages(consumerName, queueName, handlerFunc); err != nil {
		log.Fatalf("Failed to start consuming messages: %v", err)
	}
	slog.Info("Consumer is created successfully", "queueName", queueName, "consumer_name", consumerName)

	go setUpHealthCheckerAPIs(ctx, cfg, storage, rabbitClient, redisClient)

	slog.Info("Worker is running. To exit press CTRL+C", "worker_num", workerNumber)
	<-sigChan
	cancel()
	slog.Info("Worker is shutting down...", "worker_num", workerNumber)
}

func setUpHealthCheckerAPIs(ctx context.Context, cfg *configs.Config, storage domain.Storage, rabbitClient *rabbitmq.RabbitMQClient, redisClient *redis.Client) {
	r := gin.Default()
	r.GET("/readiness", func(c *gin.Context) {
		if postgresIsReady && rabbitIsReady && redisIsReady {
			c.JSON(http.StatusOK, gin.H{"status": "ready"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
	})
	r.GET("/liveness", func(c *gin.Context) {
		if err := storage.Ping(ctx); err != nil {
			slog.Error("Postgresql seem not to be pingable in liveness API", "error", err.Error())
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not healthy"})
			return
		}

		if !rabbitClient.IsHealthy() {
			slog.Error("Rabbit is not healthy")
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not healthy"})
			return
		}

		if err := redisClient.Ping(ctx); err != nil {
			slog.Error("Redis seem not to be pingable in liveness API", "error", err.Error())
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not healthy"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "up"})
	})

	if cfg.MetricsEnabled {
		r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	}

	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: r,
	}

	go func() {
		log.Printf("Starting server on port %s\n", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down health server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
