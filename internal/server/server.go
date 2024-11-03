package server

import (
	"context"
	"encoding/json"
	"github.com/sf7293/task-manager/internal/domain"
	"github.com/sf7293/task-manager/internal/errval"
	"log/slog"
)

type ServerLogic struct {
	storage                     domain.Storage
	queueClient                 domain.Queue
	highPriorityJobsQueueName   string
	normalPriorityJobsQueueName string
	lowPriorityJobsQueueName    string
}

func NewServerLogic(storage domain.Storage, queueClient domain.Queue, highPriorityJobsQueueName, normalJobsQueueName, lowPriorityJobsQueueName string) *ServerLogic {
	return &ServerLogic{
		storage:                     storage,
		queueClient:                 queueClient,
		highPriorityJobsQueueName:   highPriorityJobsQueueName,
		normalPriorityJobsQueueName: normalJobsQueueName,
		lowPriorityJobsQueueName:    lowPriorityJobsQueueName,
	}
}

func (s *ServerLogic) AddTask(ctx context.Context, req domain.RouterRequestAddTask) (taskID int32, err error) {
	marshalledPayload, err := json.Marshal(req.Payload)
	if err != nil {
		slog.Error("error while marshalling request payload", "err", err.Error())
		return -1, errval.ErrInternal
	}

	taskPriority := string(domain.Normal)
	if req.TaskPriority != nil {
		taskPriority = *req.TaskPriority
	}

	task, err := s.storage.InsertTask(ctx, req.Name, req.TaskType, string(domain.Queued), taskPriority, string(marshalledPayload))
	if err != nil {
		slog.ErrorContext(ctx, "error occurred while calling storage.InsertTask", "error", err)
		return -1, errval.ErrInternal
	}

	marshalledTask, err := json.Marshal(task)
	if err != nil {
		slog.Error("There was an error in marshalling newly created task", "error", err.Error())
		// I've ignored returning the error here and just log it because I'll handle re-queueing task again in another worker
		return task.ID, nil
	}

	queueName := s.normalPriorityJobsQueueName
	switch taskPriority {
	case string(domain.High):
		queueName = s.highPriorityJobsQueueName
	case string(domain.Low):
		queueName = s.lowPriorityJobsQueueName
	}
	err = s.queueClient.PublishMessage(queueName, string(marshalledTask))
	if err != nil {
		slog.Error("Error occurred while queuing marshalled task to jobs queue", "error", err.Error())
		// Again I've ignored returning the error here and just log it because I'll handle re-queueing task again in another worker
	}

	return task.ID, nil
}

func (s *ServerLogic) GetTaskStatus(ctx context.Context, taskID int32) (status string, err error) {
	task, err := s.storage.GetTaskByID(ctx, taskID)
	if err != nil {
		if err == errval.ErrNotFound {
			slog.Info("task not found with the given id", "id", taskID)
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
			slog.Info("history not found for the given task id", "task_id", taskID)
			return nil, err
		}

		slog.ErrorContext(ctx, "error occurred while calling storage.GetTaskStatusChangeHistory", "error", err)
		return nil, errval.ErrInternal
	}

	return taskHistory, nil
}
