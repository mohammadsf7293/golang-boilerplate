package domain

type TaskStatusChangeHistory struct {
	ID             int32  `json:"-"`
	TaskID         int32  `json:"task_id"`
	OldStatus      string `json:"old_status"`
	NewStatus      string `json:"new_status"`
	CreatedAtStamp int64  `json:"created_at_stamp"`
}
