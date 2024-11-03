package domain

import "context"

type Storage interface {
	Ping(ctx context.Context) (err error)
	GetTaskByID(ctx context.Context, ID int32) (*Task, error)
	GetLimitedTasksByStatus(ctx context.Context, taskStatus string, limit int32) ([]*Task, error)
	GetMissedTasks(ctx context.Context, taskStatus string, passedSeconds, limit int32) ([]*Task, error)
	GetTasksByStatus(ctx context.Context, taskStatus string) ([]*Task, error)
	GetTaskStatusChangeHistory(ctx context.Context, taskID int32) ([]*TaskStatusChangeHistory, error)
	InsertTask(ctx context.Context, name string, taskType, taskStatus, taskPriority, payload string) (task *Task, err error)
	UpdateTaskStatusAndLogChangeInTx(ctx context.Context, taskID int32, currentStatus, newStatus string) (err error)
}
