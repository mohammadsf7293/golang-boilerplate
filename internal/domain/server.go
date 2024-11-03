package domain

type RouterRequestAddTask struct {
	Name         string  `json:"name" form:"name" binding:"required"`
	TaskType     string  `json:"type" form:"type" binding:"required,validate_task_type"`
	TaskPriority *string `json:"priority" form:"priority" binding:"omitempty,validate_priority"`
	Payload      string  `json:"payload" binding:"required,validate_payload"`
}
