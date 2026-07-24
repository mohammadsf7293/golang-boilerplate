package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/mohammadsf7293/golang-boilerplate/configs"
	db2 "github.com/mohammadsf7293/golang-boilerplate/db"
	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
	"github.com/mohammadsf7293/golang-boilerplate/internal/errval"
	"github.com/mohammadsf7293/golang-boilerplate/internal/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
)

func setupStorage(t *testing.T) (context.Context, domain.Storage) {
	t.Helper()
	cfg := configs.InitConfig()
	if cfg.Database.Host == "" || cfg.Database.DatabaseTest == "" {
		t.Skip("database config not available; skipping integration test")
	}
	ctx := context.Background()

	d, err := iofs.New(db2.Migrations, "migrations")
	require.NoError(t, err)
	m, err := migrate.NewWithSourceInstance("iofs", d, cfg.Database.ToTestMigrationUri())
	if err != nil {
		t.Skipf("unable to init test migrations: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Skipf("unable to run test migrations: %v", err)
	}

	storage, err := postgres.NewStorage(ctx, cfg.Database.ToTestDBConnectionUri())
	if err != nil {
		t.Skipf("unable to connect to test database: %v", err)
	}
	return ctx, storage
}

func TestInsertTaskWithOutboxAndCAS(t *testing.T) {
	ctx, storage := setupStorage(t)

	task, messageID, err := storage.InsertTaskWithOutbox(
		ctx,
		"outbox-task",
		string(domain.SendEmail),
		string(domain.Queued),
		string(domain.High),
		`{"to":"a@b.com"}`,
		"jobs_test",
	)
	require.NoError(t, err)
	assert.Greater(t, task.ID, int32(0))
	assert.Equal(t, string(domain.High), task.Priority)
	_, err = uuid.Parse(messageID)
	require.NoError(t, err)

	entries, err := storage.GetUnpublishedOutbox(ctx, 10)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	found := false
	for _, entry := range entries {
		if entry.MessageID == messageID {
			found = true
			assert.Equal(t, "jobs_test", entry.QueueName)
			var payloadTask domain.Task
			require.NoError(t, json.Unmarshal([]byte(entry.Payload), &payloadTask))
			assert.Equal(t, task.ID, payloadTask.ID)
			assert.Equal(t, string(domain.High), payloadTask.Priority)
		}
	}
	assert.True(t, found)

	require.NoError(t, storage.MarkOutboxPublished(ctx, entries[0].ID))

	err = storage.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, string(domain.Queued), string(domain.Running))
	require.NoError(t, err)

	err = storage.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, string(domain.Queued), string(domain.Running))
	assert.ErrorIs(t, err, errval.ErrConcurrentUpdate)

	err = storage.UpdateTaskStatusWithInboxInTx(ctx, task.ID, string(domain.Running), string(domain.Succeeded), messageID)
	require.NoError(t, err)

	processed, err := storage.IsMessageProcessed(ctx, messageID)
	require.NoError(t, err)
	assert.True(t, processed)

	require.NoError(t, storage.RecordInboxMessage(ctx, messageID, task.ID))
}

func TestReclaimStaleRunningTasks(t *testing.T) {
	ctx, storage := setupStorage(t)

	task, _, err := storage.InsertTaskWithOutbox(
		ctx,
		"running-task",
		string(domain.RunQuery),
		string(domain.Queued),
		string(domain.Normal),
		`{"q":"select 1"}`,
		"jobs_test",
	)
	require.NoError(t, err)
	require.NoError(t, storage.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, string(domain.Queued), string(domain.Running)))

	// Zero seconds means any running task is considered stale for this test.
	reclaimed, err := storage.ReclaimStaleRunningTasks(ctx, 0, 100)
	require.NoError(t, err)

	found := false
	for _, item := range reclaimed {
		if item.ID == task.ID {
			found = true
			assert.Equal(t, string(domain.Queued), item.Status)
		}
	}
	assert.True(t, found)
}
