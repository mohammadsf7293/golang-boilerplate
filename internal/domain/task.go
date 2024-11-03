package domain

type TaskStatus string

const (
	Queued    TaskStatus = "queued"
	Running   TaskStatus = "running"
	Failed    TaskStatus = "failed"
	Succeeded TaskStatus = "succeeded"
)

type TaskType string

const (
	SendEmail TaskType = "send_email"
	RunQuery  TaskType = "run_query"
)

type TaskPriority string

const (
	High   TaskPriority = "high"
	Normal TaskPriority = "normal"
	Low    TaskPriority = "low"
)

type Task struct {
	ID             int32  `json:"id"`
	Type           string `json:"type"`
	Status         string `json:"status"`
	Priority       string `json:"priority"`
	PayLoad        string `json:"payload"`
	CreatedAtStamp int64  `json:"created_at_stamp"`
	UpdatedAtStamp int64  `json:"updated_at_stamp"`
}
