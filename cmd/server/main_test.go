package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/sf7293/task-manager/configs"
	db2 "github.com/sf7293/task-manager/db"
	"github.com/sf7293/task-manager/internal/postgres"
	"github.com/sf7293/task-manager/internal/rabbitmq"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

//var m *migrate.Migrate

func TestMain(t *testing.M) {
	// Set up the environment for the tests
	cfg := configs.InitConfig()

	// Setup: Run migrations up
	d, err := iofs.New(db2.Migrations, "migrations")
	if err != nil {
		log.Fatal("Error while preparing migrations, error: " + err.Error())
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, cfg.Database.ToTestMigrationUri())
	if err != nil {
		log.Fatal("Error while creating new iofs source instance for migrations, error: " + err.Error())
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatal("Error while running migrations, error: " + err.Error())
	}

	slog.Info("Migrations ran successfully")

	// Run the tests
	_ = m.Run()

	// Teardown: Run migrations down
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatal("Error while rolling back migrations, error: " + err.Error())
	}
	//TODO: for future tests, you would also need to flush RabbitMQ and Redis

	slog.Info("Migrations rolled back successfully")
}

func runTestServer() *httptest.Server {
	cfg := configs.InitConfig()

	ctx := context.Background()
	storage, err := postgres.NewStorage(ctx, cfg.Database.ToTestDBConnectionUri())
	if err != nil {
		log.Fatal(err)
	}
	slog.Info("Postgres connection has been initialized successfully")

	mainQueueNames := cfg.RabbitMQ.GetMainQueueNamesForTest()
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

	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))

	// I have considered all the queues as one test queue
	// TODO: have different test jobs queue for each priority and test whether the workers work true for each priority or not
	return httptest.NewServer(setupHTTPServer(storage, rabbitClient, cfg.RabbitMQ.TestJobsQueueName, cfg.RabbitMQ.TestJobsQueueName, cfg.RabbitMQ.TestJobsQueueName))
}

func Test_liveness_api(t *testing.T) {
	ts := runTestServer()
	defer ts.Close()

	t.Run("it should return 200 when health is ok", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/liveness", ts.URL))

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		assert.Equal(t, 200, resp.StatusCode)
	})
}

func Test_readiness_api(t *testing.T) {
	ts := runTestServer()
	defer ts.Close()

	t.Run("it should return 200 when health is ok", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/readiness", ts.URL))

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		assert.Equal(t, 200, resp.StatusCode)
	})
}

func Test_create_task_api(t *testing.T) {
	ts := runTestServer()
	defer ts.Close()

	t.Run("it should return 200 when health is ok", func(t *testing.T) {
		// Define the request payload as a map
		payload := map[string]interface{}{
			"name":    "sample_task_1",
			"type":    "send_email",
			"payload": `{"item1":"value1"}`,
		}

		// Convert the payload to JSON
		jsonData, err := json.Marshal(payload)
		if err != nil {
			slog.Error("Error marshalling JSON:", "error", err.Error())
			return
		}

		req, err := http.NewRequest("POST", fmt.Sprintf("%s/tasks", ts.URL), bytes.NewBuffer(jsonData))
		if err != nil {
			slog.Error("Error creating request", "error", err.Error())
			return
		}

		// Set the Content-Type header
		req.Header.Set("Content-Type", "application/json")

		// Create an HTTP client and execute the request
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			slog.Error("Error sending request", "error", err)
			return
		}
		defer func() {
			err = resp.Body.Close()
			if err != nil {
				slog.Error("Error while closing response body", "error", err.Error())
				return
			}
		}()

		assert.Equal(t, 200, resp.StatusCode)
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatal("Error while reading response body, error: " + err.Error())
		}
		responseMap := map[string]int{}
		err = json.Unmarshal(body, &responseMap)
		if err != nil {
			log.Fatal("Error while unmarshalling response body, error: " + err.Error())
		}

		addedTaskId, exists := responseMap["added_task_id"]
		assert.Equal(t, true, exists)
		// Making sure the task id is 1
		assert.Equal(t, 1, addedTaskId)
	})
}

// TODO for tests:
// 1 - Development of tests fo other APIs (all APIs)
// 2 - Development of tests for running job worker and checking that:
// - Status of tasks are changed
// - History of changes are available in db (or by checking /tasks/:id/history) API
