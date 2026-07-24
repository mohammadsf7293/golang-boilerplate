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
	InsertTaskWithOutbox(ctx context.Context, name string, taskType, taskStatus, taskPriority, payload, queueName string) (task *Task, messageID string, err error)
	UpdateTaskStatusAndLogChangeInTx(ctx context.Context, taskID int32, currentStatus, newStatus string) (err error)
	UpdateTaskStatusWithInboxInTx(ctx context.Context, taskID int32, currentStatus, newStatus, messageID string) (err error)
	IsMessageProcessed(ctx context.Context, messageID string) (bool, error)
	RecordInboxMessage(ctx context.Context, messageID string, taskID int32) error
	GetUnpublishedOutbox(ctx context.Context, limit int32) ([]*OutboxEntry, error)
	MarkOutboxPublished(ctx context.Context, id int64) error
	EnqueueTaskToOutbox(ctx context.Context, task *Task, queueName string) (messageID string, err error)
	ReclaimStaleRunningTasks(ctx context.Context, passedSeconds, limit int32) ([]*Task, error)
}
