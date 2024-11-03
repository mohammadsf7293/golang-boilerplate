package postgres

import (
	"context"
	"github.com/cenkalti/backoff/v4"
	"github.com/jackc/pgtype"
	"github.com/sf7293/task-manager/internal/domain"
	"github.com/sf7293/task-manager/internal/errval"
	"log/slog"
	"strings"
	"time"

	_ "github.com/cenkalti/backoff/v4"
	"github.com/jackc/pgx/v4/pgxpool"
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

	castedTask := convertTask(task)
	return castedTask, err
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

	convertedTasks := convertTasks(tasks)
	return convertedTasks, nil
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

	convertedTasks := convertTasks(tasks)
	return convertedTasks, nil
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

	convertedTasks := convertTasks(tasks)
	return convertedTasks, nil
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

	convertedTasks := convertTaskStatusChangeHistories(taskStatusChangeHistory)
	return convertedTasks, nil
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
		Type:           string(taskType),
		Status:         taskStatus,
		PayLoad:        payload,
		CreatedAtStamp: nowStamp,
		UpdatedAtStamp: nowStamp,
	}

	return task, err
}

func (s *storage) UpdateTaskStatusAndLogChangeInTx(ctx context.Context, taskID int32, currentStatus, newStatus string) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}

	qtx := s.queries.WithTx(tx)
	err = qtx.UpdateTaskStatus(ctx, UpdateTaskStatusParams{
		ID:     taskID,
		Status: TaskStatus(newStatus),
	})
	if err != nil {
		err2 := tx.Rollback(ctx)
		if err2 != nil {
			slog.Error("Error occurred while rolling back transaction", "error", err2.Error())
		}

		return err
	}

	err = qtx.InsertTaskStatusChangeHistory(ctx, InsertTaskStatusChangeHistoryParams{
		TaskID:    taskID,
		OldStatus: TaskStatus(currentStatus),
		NewStatus: TaskStatus(newStatus),
	})
	if err != nil {
		err2 := tx.Rollback(ctx)
		if err2 != nil {
			slog.Error("Error occurred while rolling back transaction", "error", err2.Error())
		}

		return err
	}

	return tx.Commit(ctx)
}

func (s *storage) Ping(ctx context.Context) (err error) {
	return s.pool.Ping(ctx)
}

func convertTask(task Task) *domain.Task {
	castedItem := &domain.Task{
		ID:             task.ID,
		Type:           string(task.Type),
		Status:         string(task.Status),
		Priority:       string(task.Priority),
		PayLoad:        string(task.Payload.Bytes),
		CreatedAtStamp: task.CreatedAt.Time.Unix(),
		UpdatedAtStamp: task.CreatedAt.Time.Unix(),
	}

	return castedItem
}

func convertTasks(tasks []Task) []*domain.Task {
	castedTasks := []*domain.Task{}
	for _, item := range tasks {
		castedTask := convertTask(item)
		castedTasks = append(castedTasks, castedTask)
	}

	return castedTasks
}

func convertTaskStatusChangeHistory(item TasksStatusChangeHistory) *domain.TaskStatusChangeHistory {
	castedItem := &domain.TaskStatusChangeHistory{
		ID:             item.ID,
		TaskID:         item.TaskID,
		OldStatus:      string(item.OldStatus),
		NewStatus:      string(item.NewStatus),
		CreatedAtStamp: item.CreatedAt.Time.Unix(),
	}

	return castedItem
}

func convertTaskStatusChangeHistories(items []TasksStatusChangeHistory) []*domain.TaskStatusChangeHistory {
	castedItems := []*domain.TaskStatusChangeHistory{}
	for _, item := range items {
		castedItem := convertTaskStatusChangeHistory(item)
		castedItems = append(castedItems, castedItem)
	}

	return castedItems
}
