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
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/mohammadsf7293/golang-boilerplate/configs"
	db2 "github.com/mohammadsf7293/golang-boilerplate/db"
	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
	"github.com/mohammadsf7293/golang-boilerplate/internal/errval"
	"github.com/mohammadsf7293/golang-boilerplate/internal/outbox"
	"github.com/mohammadsf7293/golang-boilerplate/internal/postgres"
	"github.com/mohammadsf7293/golang-boilerplate/internal/rabbitmq"
	"github.com/mohammadsf7293/golang-boilerplate/internal/server"
	"github.com/mohammadsf7293/golang-boilerplate/internal/server/middleware"

	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

var postgresIsReady, rabbitIsReady bool

func main() {
	cfg := configs.InitConfig()

	d, err := iofs.New(db2.Migrations, "migrations")
	if err != nil {
		log.Fatal(err)
		return
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, cfg.Database.ToMigrationUri())
	if err != nil {
		log.Fatal(err)
		return
	}

	if err := m.Up(); err != nil {
		if !errors.Is(err, migrate.ErrNoChange) {
			log.Fatal(err)
		}
	}
	slog.Info("Migrations ran successfully")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	storage, err := postgres.NewStorage(ctx, cfg.Database.ToDbConnectionUri())
	if err != nil {
		log.Fatal(err)
	}
	postgresIsReady = true
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
	rabbitIsReady = true
	slog.Info("RabbitMQ has been initialized successfully")

	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))

	relay := outbox.NewRelay(storage, rabbitClient, cfg.OutboxBatchSize, time.Duration(cfg.OutboxPollIntervalMs)*time.Millisecond)
	go relay.Run(ctx)

	router := setupHTTPServer(storage, rabbitClient, cfg)
	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: router,
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
	log.Println("Shutting down server...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServerTimeOutInSeconds)*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exiting")
}

func setupHTTPServer(storage domain.Storage, rabbitClient *rabbitmq.RabbitMQClient, cfg *configs.Config) *gin.Engine {
	r := gin.Default()
	r.Use(middleware.RequestID())

	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		if err := v.RegisterValidation("validate_task_type", validateTaskType); err != nil {
			log.Fatal("failed to bind validation rule of validate_task_type")
		}
		if err := v.RegisterValidation("validate_priority", validatePriority); err != nil {
			log.Fatal("failed to bind validation rule of validate_priority")
		}
		if err := v.RegisterValidation("validate_payload", validatePayload); err != nil {
			log.Fatal("failed to bind validation rule of validate_payload")
		}
	}

	serverLogic := server.NewServerLogic(storage, cfg.RabbitMQ.HighPriorityJobsQueueName, cfg.RabbitMQ.NormalPriorityJobsQueueName, cfg.RabbitMQ.LowPriorityJobsQueueName)
	tasks := r.Group("/tasks")
	tasks.POST("", func(c *gin.Context) {
		req := domain.RouterRequestAddTask{}
		if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
			slog.Error("error occurred while binding request", "error", err, "request_id", middleware.GetRequestID(c))
			middleware.BadRequest(c, "invalid request body")
			return
		}

		addedTaskID, err := serverLogic.AddTask(c.Request.Context(), req)
		if err != nil {
			middleware.InternalError(c, err.Error())
			return
		}

		c.JSON(http.StatusOK, gin.H{"added_task_id": addedTaskID})
	})

	tasks.GET("/:id", func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			slog.Error("Invalid id parameter", "error", err, "request_id", middleware.GetRequestID(c))
			middleware.BadRequest(c, "Invalid id")
			return
		}

		taskStatus, err := serverLogic.GetTaskStatus(c.Request.Context(), int32(id))
		if err != nil {
			if err == errval.ErrNotFound {
				middleware.NotFound(c, "task not found")
				return
			}
			middleware.InternalError(c, "internal server error")
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": taskStatus})
	})

	tasks.GET("/:id/history", func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			slog.Error("Invalid id parameter", "error", err, "request_id", middleware.GetRequestID(c))
			middleware.BadRequest(c, "Invalid id")
			return
		}

		taskHistory, err := serverLogic.GetTaskStatusHistory(c.Request.Context(), int32(id))
		if err != nil {
			if err == errval.ErrNotFound {
				middleware.NotFound(c, "history not found")
				return
			}
			middleware.InternalError(c, "internal server error")
			return
		}

		c.JSON(http.StatusOK, gin.H{"history": taskHistory})
	})

	r.GET("/readiness", func(c *gin.Context) {
		if postgresIsReady && rabbitIsReady {
			c.JSON(http.StatusOK, gin.H{"status": "ready"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
	})
	r.GET("/liveness", func(c *gin.Context) {
		if err := storage.Ping(c.Request.Context()); err != nil {
			slog.Error("Postgresql seem not to be pingable in liveness API", "error", err.Error())
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not healthy"})
			return
		}

		if !rabbitClient.IsHealthy() {
			slog.Error("Rabbit is not healthy")
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not healthy"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "up"})
	})

	if cfg.MetricsEnabled {
		r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	}

	return r
}

var validateTaskType validator.Func = func(fl validator.FieldLevel) bool {
	taskType := fl.Field().String()
	switch taskType {
	case string(domain.SendEmail), string(domain.RunQuery):
		return true
	default:
		return false
	}
}

var validatePayload validator.Func = func(fl validator.FieldLevel) bool {
	payloadStr := fl.Field().String()
	if payloadStr == "null" {
		return false
	}

	unmarshalledPayload := map[string]string{}
	err := json.Unmarshal([]byte(payloadStr), &unmarshalledPayload)
	if err != nil {
		slog.Error("An error occurred while unmarshalling payload to map[string]string", "error", err.Error())
		return false
	}

	if len(unmarshalledPayload) == 0 {
		return false
	}

	return true
}

var validatePriority validator.Func = func(fl validator.FieldLevel) bool {
	taskPriority := fl.Field().String()
	switch taskPriority {
	case string(domain.High), string(domain.Normal), string(domain.Low):
		return true
	default:
		return false
	}
}
