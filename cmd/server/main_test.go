package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/mohammadsf7293/golang-boilerplate/configs"
	db2 "github.com/mohammadsf7293/golang-boilerplate/db"
	"github.com/mohammadsf7293/golang-boilerplate/internal/postgres"
	"github.com/mohammadsf7293/golang-boilerplate/internal/rabbitmq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func TestMain(m *testing.M) {
	cfg := configs.InitConfig()

	d, err := iofs.New(db2.Migrations, "migrations")
	if err != nil {
		log.Fatal("Error while preparing migrations, error: " + err.Error())
	}

	migrator, err := migrate.NewWithSourceInstance("iofs", d, cfg.Database.ToTestMigrationUri())
	if err != nil {
		log.Fatal("Error while creating new iofs source instance for migrations, error: " + err.Error())
	}

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatal("Error while running migrations, error: " + err.Error())
	}

	slog.Info("Migrations ran successfully")

	code := m.Run()

	if err := migrator.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatal("Error while rolling back migrations, error: " + err.Error())
	}

	slog.Info("Migrations rolled back successfully")
	os.Exit(code)
}

func runTestServer() *httptest.Server {
	cfg := configs.InitConfig()
	cfg.RabbitMQ.HighPriorityJobsQueueName = cfg.RabbitMQ.TestJobsQueueName
	cfg.RabbitMQ.NormalPriorityJobsQueueName = cfg.RabbitMQ.TestJobsQueueName
	cfg.RabbitMQ.LowPriorityJobsQueueName = cfg.RabbitMQ.TestJobsQueueName
	cfg.MetricsEnabled = false

	ctx := context.Background()
	storage, err := postgres.NewStorage(ctx, cfg.Database.ToTestDBConnectionUri())
	if err != nil {
		log.Fatal(err)
	}
	slog.Info("Postgres connection has been initialized successfully")

	mainQueueNames := cfg.RabbitMQ.GetMainQueueNamesForTest()
	rabbitClient, err := rabbitmq.NewRabbitMQClient(ctx, cfg.RabbitMQ.ToRabbitConnectionUri(), mainQueueNames, rabbitmq.ClientOptions{
		PrefetchCount: cfg.RabbitPrefetchCount,
		DLXName:       cfg.RabbitMQ.DeadLetterExchange,
		DLQName:       cfg.RabbitMQ.DeadLetterQueueName,
		MaxRetries:    cfg.RabbitMaxRetries,
	})
	if err != nil {
		log.Fatal(err)
	}

	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))

	return httptest.NewServer(setupHTTPServer(storage, rabbitClient, cfg))
}

func Test_liveness_api(t *testing.T) {
	ts := runTestServer()
	defer ts.Close()

	t.Run("it should return 200 when health is ok", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/liveness", ts.URL))
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
	})
}

func Test_readiness_api(t *testing.T) {
	ts := runTestServer()
	defer ts.Close()

	t.Run("it should return 200 when health is ok", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/readiness", ts.URL))
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
	})
}

func Test_create_task_api(t *testing.T) {
	ts := runTestServer()
	defer ts.Close()

	t.Run("it should return 200 and create task via outbox", func(t *testing.T) {
		payload := map[string]interface{}{
			"name":    "sample_task_1",
			"type":    "send_email",
			"payload": `{"item1":"value1"}`,
		}

		jsonData, err := json.Marshal(payload)
		require.NoError(t, err)

		req, err := http.NewRequest("POST", fmt.Sprintf("%s/tasks", ts.URL), bytes.NewBuffer(jsonData))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, 200, resp.StatusCode)
		assert.NotEmpty(t, resp.Header.Get("X-Request-ID"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		responseMap := map[string]int{}
		err = json.Unmarshal(body, &responseMap)
		require.NoError(t, err)

		addedTaskID, exists := responseMap["added_task_id"]
		assert.True(t, exists)
		assert.Greater(t, addedTaskID, 0)

		statusResp, err := http.Get(fmt.Sprintf("%s/tasks/%d", ts.URL, addedTaskID))
		require.NoError(t, err)
		defer statusResp.Body.Close()
		assert.Equal(t, 200, statusResp.StatusCode)
	})
}

func Test_create_task_validation_error_envelope(t *testing.T) {
	ts := runTestServer()
	defer ts.Close()

	payload := map[string]interface{}{
		"name": "bad_task",
		"type": "invalid_type",
	}
	jsonData, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/tasks", ts.URL), bytes.NewBuffer(jsonData))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var errBody map[string]string
	require.NoError(t, json.Unmarshal(body, &errBody))
	assert.Equal(t, "invalid request body", errBody["error"])
	assert.NotEmpty(t, errBody["request_id"])
}
