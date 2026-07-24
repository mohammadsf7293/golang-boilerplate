package postgres

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgtype"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
	"github.com/mohammadsf7293/golang-boilerplate/internal/errval"
)

type storage struct {
	queries *Queries
	pool    *pgxpool.Pool
}

func NewStorage(ctx context.Context, dsn string) (*storage, error) {
	var pool *pgxpool.Pool
	var err error

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	err = backoff.Retry(func() error {
		if pool, err = pgxpool.ConnectConfig(ctx, config); err != nil {
			slog.ErrorContext(ctx, "failed to connect to postgres database.. retrying...", "error", err)
			return err
		}

		if err = pool.Ping(ctx); err != nil {
			slog.ErrorContext(ctx, "failed to ping postgres database connection.. retrying...", "error", err)
			return err
		}

		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(3*time.Second), 5))

	if err != nil {
		return nil, err
	}

	return &storage{
		queries: New(pool),
		pool:    pool,
	}, nil
}

func (s *storage) GetTaskByID(ctx context.Context, ID int32) (*domain.Task, error) {
	task, err := s.queries.GetTaskByID(ctx, ID)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, errval.ErrNotFound
		}

		return nil, err
	}

	return convertTask(task), nil
}

func (s *storage) GetLimitedTasksByStatus(ctx context.Context, taskStatus string, limit int32) ([]*domain.Task, error) {
	tasks, err := s.queries.GetLimitedTasksByStatus(ctx, GetLimitedTasksByStatusParams{
		Status: TaskStatus(taskStatus),
		Limit:  limit,
	})
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, errval.ErrNotFound
		}

		return nil, err
	}

	if len(tasks) == 0 {
		return nil, errval.ErrNotFound
	}

	return convertTasks(tasks), nil
}

func (s *storage) GetMissedTasks(ctx context.Context, taskStatus string, passedSeconds, limit int32) ([]*domain.Task, error) {
	tasks, err := s.queries.GetMissedTasks(ctx, GetMissedTasksParams{
		Status:  TaskStatus(taskStatus),
		Column2: passedSeconds,
		Limit:   limit,
	})
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, errval.ErrNotFound
		}

		return nil, err
	}

	if len(tasks) == 0 {
		return nil, errval.ErrNotFound
	}

	return convertTasks(tasks), nil
}

func (s *storage) GetTasksByStatus(ctx context.Context, taskStatus string) ([]*domain.Task, error) {
	tasks, err := s.queries.GetTasksByStatus(ctx, TaskStatus(taskStatus))
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, errval.ErrNotFound
		}

		return nil, err
	}

	if len(tasks) == 0 {
		return nil, errval.ErrNotFound
	}

	return convertTasks(tasks), nil
}

func (s *storage) GetTaskStatusChangeHistory(ctx context.Context, taskID int32) ([]*domain.TaskStatusChangeHistory, error) {
	taskStatusChangeHistory, err := s.queries.GetTaskStatusChangeHistory(ctx, taskID)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, errval.ErrNotFound
		}

		return nil, err
	}

	if len(taskStatusChangeHistory) == 0 {
		return nil, errval.ErrNotFound
	}

	return convertTaskStatusChangeHistories(taskStatusChangeHistory), nil
}

func (s *storage) InsertTask(ctx context.Context, name string, taskType, taskStatus, taskPriority, payload string) (task *domain.Task, err error) {
	jsonBytes := []byte(payload)

	var payloadJSON pgtype.JSON
	if err := payloadJSON.Set(jsonBytes); err != nil {
		return nil, err
	}

	taskID, err := s.queries.InsertTask(ctx, InsertTaskParams{
		Name:     name,
		Type:     TaskType(taskType),
		Status:   TaskStatus(taskStatus),
		Priority: TaskPriority(taskPriority),
		Payload:  payloadJSON,
	})
	if err != nil {
		return nil, err
	}

	nowStamp := time.Now().UTC().Unix()
	task = &domain.Task{
		ID:             taskID,
		Type:           taskType,
		Status:         taskStatus,
		Priority:       taskPriority,
		PayLoad:        payload,
		CreatedAtStamp: nowStamp,
		UpdatedAtStamp: nowStamp,
	}

	return task, nil
}

func (s *storage) InsertTaskWithOutbox(ctx context.Context, name string, taskType, taskStatus, taskPriority, payload, queueName string) (*domain.Task, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := s.queries.WithTx(tx)

	var payloadJSON pgtype.JSON
	if err := payloadJSON.Set([]byte(payload)); err != nil {
		return nil, "", err
	}

	taskID, err := qtx.InsertTask(ctx, InsertTaskParams{
		Name:     name,
		Type:     TaskType(taskType),
		Status:   TaskStatus(taskStatus),
		Priority: TaskPriority(taskPriority),
		Payload:  payloadJSON,
	})
	if err != nil {
		return nil, "", err
	}

	nowStamp := time.Now().UTC().Unix()
	task := &domain.Task{
		ID:             taskID,
		Type:           taskType,
		Status:         taskStatus,
		Priority:       taskPriority,
		PayLoad:        payload,
		CreatedAtStamp: nowStamp,
		UpdatedAtStamp: nowStamp,
	}

	marshalledTask, err := json.Marshal(task)
	if err != nil {
		return nil, "", err
	}

	messageID := uuid.New()
	var outboxPayload pgtype.JSONB
	if err := outboxPayload.Set(marshalledTask); err != nil {
		return nil, "", err
	}

	_, err = qtx.InsertOutbox(ctx, InsertOutboxParams{
		MessageID: messageID,
		QueueName: queueName,
		Payload:   outboxPayload,
	})
	if err != nil {
		return nil, "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}

	return task, messageID.String(), nil
}

func (s *storage) UpdateTaskStatusAndLogChangeInTx(ctx context.Context, taskID int32, currentStatus, newStatus string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := s.queries.WithTx(tx)
	rows, err := qtx.UpdateTaskStatusCAS(ctx, UpdateTaskStatusCASParams{
		Status:   TaskStatus(newStatus),
		ID:       taskID,
		Status_2: TaskStatus(currentStatus),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return errval.ErrConcurrentUpdate
	}

	err = qtx.InsertTaskStatusChangeHistory(ctx, InsertTaskStatusChangeHistoryParams{
		TaskID:    taskID,
		OldStatus: TaskStatus(currentStatus),
		NewStatus: TaskStatus(newStatus),
	})
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *storage) UpdateTaskStatusWithInboxInTx(ctx context.Context, taskID int32, currentStatus, newStatus, messageID string) error {
	parsedMessageID, err := uuid.Parse(messageID)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := s.queries.WithTx(tx)
	rows, err := qtx.UpdateTaskStatusCAS(ctx, UpdateTaskStatusCASParams{
		Status:   TaskStatus(newStatus),
		ID:       taskID,
		Status_2: TaskStatus(currentStatus),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return errval.ErrConcurrentUpdate
	}

	err = qtx.InsertTaskStatusChangeHistory(ctx, InsertTaskStatusChangeHistoryParams{
		TaskID:    taskID,
		OldStatus: TaskStatus(currentStatus),
		NewStatus: TaskStatus(newStatus),
	})
	if err != nil {
		return err
	}

	err = qtx.InsertInbox(ctx, InsertInboxParams{
		MessageID: parsedMessageID,
		TaskID:    taskID,
	})
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *storage) IsMessageProcessed(ctx context.Context, messageID string) (bool, error) {
	parsedMessageID, err := uuid.Parse(messageID)
	if err != nil {
		return false, err
	}
	return s.queries.IsMessageProcessed(ctx, parsedMessageID)
}

func (s *storage) RecordInboxMessage(ctx context.Context, messageID string, taskID int32) error {
	parsedMessageID, err := uuid.Parse(messageID)
	if err != nil {
		return err
	}
	return s.queries.InsertInbox(ctx, InsertInboxParams{
		MessageID: parsedMessageID,
		TaskID:    taskID,
	})
}

func (s *storage) GetUnpublishedOutbox(ctx context.Context, limit int32) ([]*domain.OutboxEntry, error) {
	rows, err := s.queries.GetUnpublishedOutbox(ctx, limit)
	if err != nil {
		return nil, err
	}

	entries := make([]*domain.OutboxEntry, 0, len(rows))
	for _, row := range rows {
		payload := string(row.Payload.Bytes)
		if payload == "" && row.Payload.Status == pgtype.Present {
			b, err := row.Payload.MarshalJSON()
			if err != nil {
				return nil, err
			}
			payload = string(b)
		}
		entries = append(entries, &domain.OutboxEntry{
			ID:        row.ID,
			MessageID: row.MessageID.String(),
			QueueName: row.QueueName,
			Payload:   payload,
		})
	}
	return entries, nil
}

func (s *storage) MarkOutboxPublished(ctx context.Context, id int64) error {
	return s.queries.MarkOutboxPublished(ctx, id)
}

func (s *storage) EnqueueTaskToOutbox(ctx context.Context, task *domain.Task, queueName string) (string, error) {
	marshalledTask, err := json.Marshal(task)
	if err != nil {
		return "", err
	}

	messageID := uuid.New()
	var outboxPayload pgtype.JSONB
	if err := outboxPayload.Set(marshalledTask); err != nil {
		return "", err
	}

	_, err = s.queries.InsertOutbox(ctx, InsertOutboxParams{
		MessageID: messageID,
		QueueName: queueName,
		Payload:   outboxPayload,
	})
	if err != nil {
		return "", err
	}

	return messageID.String(), nil
}

func (s *storage) ReclaimStaleRunningTasks(ctx context.Context, passedSeconds, limit int32) ([]*domain.Task, error) {
	tasks, err := s.GetMissedTasks(ctx, string(domain.Running), passedSeconds, limit)
	if err != nil {
		return nil, err
	}

	reclaimed := make([]*domain.Task, 0, len(tasks))
	for _, task := range tasks {
		err := s.UpdateTaskStatusAndLogChangeInTx(ctx, task.ID, string(domain.Running), string(domain.Queued))
		if err != nil {
			if err == errval.ErrConcurrentUpdate {
				continue
			}
			return reclaimed, err
		}
		task.Status = string(domain.Queued)
		reclaimed = append(reclaimed, task)
	}

	return reclaimed, nil
}

func (s *storage) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func convertTask(task Task) *domain.Task {
	updatedAt := task.CreatedAt.Time
	if task.UpdatedAt.Valid {
		updatedAt = task.UpdatedAt.Time
	}

	return &domain.Task{
		ID:             task.ID,
		Type:           string(task.Type),
		Status:         string(task.Status),
		Priority:       string(task.Priority),
		PayLoad:        string(task.Payload.Bytes),
		CreatedAtStamp: task.CreatedAt.Time.Unix(),
		UpdatedAtStamp: updatedAt.Unix(),
	}
}

func convertTasks(tasks []Task) []*domain.Task {
	castedTasks := make([]*domain.Task, 0, len(tasks))
	for _, item := range tasks {
		castedTasks = append(castedTasks, convertTask(item))
	}
	return castedTasks
}

func convertTaskStatusChangeHistory(item TasksStatusChangeHistory) *domain.TaskStatusChangeHistory {
	return &domain.TaskStatusChangeHistory{
		ID:             item.ID,
		TaskID:         item.TaskID,
		OldStatus:      string(item.OldStatus),
		NewStatus:      string(item.NewStatus),
		CreatedAtStamp: item.CreatedAt.Time.Unix(),
	}
}

func convertTaskStatusChangeHistories(items []TasksStatusChangeHistory) []*domain.TaskStatusChangeHistory {
	castedItems := make([]*domain.TaskStatusChangeHistory, 0, len(items))
	for _, item := range items {
		castedItems = append(castedItems, convertTaskStatusChangeHistory(item))
	}
	return castedItems
}
