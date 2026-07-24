package server

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/mohammadsf7293/golang-boilerplate/internal/domain"
	"github.com/mohammadsf7293/golang-boilerplate/internal/errval"
	"github.com/mohammadsf7293/golang-boilerplate/internal/metrics"
)

type ServerLogic struct {
	storage                     domain.Storage
	highPriorityJobsQueueName   string
	normalPriorityJobsQueueName string
	lowPriorityJobsQueueName    string
}

func NewServerLogic(storage domain.Storage, highPriorityJobsQueueName, normalJobsQueueName, lowPriorityJobsQueueName string) *ServerLogic {
	return &ServerLogic{
		storage:                     storage,
		highPriorityJobsQueueName:   highPriorityJobsQueueName,
		normalPriorityJobsQueueName: normalJobsQueueName,
		lowPriorityJobsQueueName:    lowPriorityJobsQueueName,
	}
}

func (s *ServerLogic) AddTask(ctx context.Context, req domain.RouterRequestAddTask) (taskID int32, err error) {
	marshalledPayload, err := json.Marshal(req.Payload)
	if err != nil {
		slog.ErrorContext(ctx, "error while marshalling request payload", "err", err.Error())
		return -1, errval.ErrInternal
	}

	taskPriority := string(domain.Normal)
	if req.TaskPriority != nil {
		taskPriority = *req.TaskPriority
	}

	queueName := s.normalPriorityJobsQueueName
	switch taskPriority {
	case string(domain.High):
		queueName = s.highPriorityJobsQueueName
	case string(domain.Low):
		queueName = s.lowPriorityJobsQueueName
	}

	task, messageID, err := s.storage.InsertTaskWithOutbox(ctx, req.Name, req.TaskType, string(domain.Queued), taskPriority, string(marshalledPayload), queueName)
	if err != nil {
		slog.ErrorContext(ctx, "error occurred while calling storage.InsertTaskWithOutbox", "error", err)
		return -1, errval.ErrInternal
	}

	metrics.TasksCreatedTotal.Inc()
	slog.InfoContext(ctx, "task created and written to outbox", "task_id", task.ID, "message_id", messageID, "queue", queueName)
	return task.ID, nil
}

func (s *ServerLogic) GetTaskStatus(ctx context.Context, taskID int32) (status string, err error) {
	task, err := s.storage.GetTaskByID(ctx, taskID)
	if err != nil {
		if err == errval.ErrNotFound {
			slog.InfoContext(ctx, "task not found with the given id", "id", taskID)
			return "", err
		}

		slog.ErrorContext(ctx, "error occurred while calling storage.GetTaskByID", "error", err)
		return "", errval.ErrInternal
	}

	return task.Status, nil
}

func (s *ServerLogic) GetTaskStatusHistory(ctx context.Context, taskID int32) (history []*domain.TaskStatusChangeHistory, err error) {
	taskHistory, err := s.storage.GetTaskStatusChangeHistory(ctx, taskID)
	if err != nil {
		if err == errval.ErrNotFound {
			slog.InfoContext(ctx, "history not found for the given task id", "task_id", taskID)
			return nil, err
		}

		slog.ErrorContext(ctx, "error occurred while calling storage.GetTaskStatusChangeHistory", "error", err)
		return nil, errval.ErrInternal
	}

	return taskHistory, nil
}
