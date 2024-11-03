package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/sf7293/task-manager/configs"
	db2 "github.com/sf7293/task-manager/db"
	"github.com/sf7293/task-manager/internal/domain"
	"github.com/sf7293/task-manager/internal/errval"
	"github.com/sf7293/task-manager/internal/postgres"
	"github.com/sf7293/task-manager/internal/rabbitmq"
	"github.com/sf7293/task-manager/internal/server"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

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

	// Setting up a context with cfg.ServerTimeOutInSeconds seconds time out, which limits the request process time with a timeout of cfg.ServerTimeOutInSeconds seconds
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ServerTimeOutInSeconds)*time.Second)
	defer cancel()

	storage, err := postgres.NewStorage(ctx, cfg.Database.ToDbConnectionUri())
	if err != nil {
		log.Fatal(err)
	}
	postgresIsReady = true
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
	rabbitIsReady = true
	slog.Info("RabbitMQ has been initialized successfully")

	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))

	// Set up a channel to handle exit signals
	exitChan := make(chan os.Signal, 1)
	signal.Notify(exitChan, syscall.SIGINT, syscall.SIGTERM)

	router := setupHTTPServer(storage, rabbitClient, cfg.RabbitMQ.HighPriorityJobsQueueName, cfg.RabbitMQ.NormalPriorityJobsQueueName, cfg.RabbitMQ.LowPriorityJobsQueueName)
	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: router,
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
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exiting")
}

func setupHTTPServer(storage domain.Storage, rabbitClient *rabbitmq.RabbitMQClient, rabbitHighPriorityJobsQueueName, rabbitNormalPriorityJobsQueueName, rabbitLowPriorityJobsQueueName string) *gin.Engine {
	r := gin.Default()
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		err := v.RegisterValidation("validate_task_type", validateTaskType)
		if err != nil {
			log.Fatal("failed to bind validation rule of validate_task_type")
		}

		err = v.RegisterValidation("validate_priority", validatePriority)
		if err != nil {
			log.Fatal("failed to bind validation rule of validate_priority")
		}

		err = v.RegisterValidation("validate_payload", validatePayload)
		if err != nil {
			log.Fatal("failed to bind validation rule of validate_payload")
		}
	}

	serverLogic := server.NewServerLogic(storage, rabbitClient, rabbitHighPriorityJobsQueueName, rabbitNormalPriorityJobsQueueName, rabbitLowPriorityJobsQueueName)
	tasks := r.Group("/tasks")
	tasks.POST("", func(c *gin.Context) {
		req := domain.RouterRequestAddTask{}
		// Request binding and validation
		err := c.ShouldBindBodyWith(&req, binding.JSON)
		if err != nil {
			slog.Error("error occurred while binding request", "error", err)
			c.JSON(http.StatusBadRequest, gin.H{})
			return
		}

		addedTaskID, err := serverLogic.AddTask(c, req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err})
			return
		}

		c.JSON(http.StatusOK, gin.H{"added_task_id": addedTaskID})
	})

	tasks.GET("/:id", func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			slog.Error("Invalid id parameter, error occurred while casting id str to int", "error", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
			return
		}

		taskStatus, err := serverLogic.GetTaskStatus(c, int32(id))
		if err != nil {
			if err == errval.ErrNotFound {
				c.JSON(http.StatusNotFound, gin.H{})
				return
			}

			c.JSON(http.StatusInternalServerError, gin.H{})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": taskStatus})
	})

	tasks.GET("/:id/history", func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			slog.Error("Invalid id parameter, error occurred while casting id str to int", "error", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
			return
		}

		taskHistory, err := serverLogic.GetTaskStatusHistory(c, int32(id))
		if err != nil {
			if err == errval.ErrNotFound {
				c.JSON(http.StatusNotFound, gin.H{})
				return
			}

			c.JSON(http.StatusInternalServerError, gin.H{})
			return
		}

		c.JSON(http.StatusOK, gin.H{"history": taskHistory})
	})

	r.GET("/readiness", func(c *gin.Context) {
		if postgresIsReady && rabbitIsReady {
			c.JSON(http.StatusOK, gin.H{"status": "ready"})
		} else {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
		}
	})
	r.GET("/liveness", func(c *gin.Context) {
		// Checking health of depending upon infra connections
		err := storage.Ping(c)
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

		c.JSON(http.StatusOK, gin.H{"status": "up"})
	})

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

	// If map must not be empty, uncomment the following rule
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
