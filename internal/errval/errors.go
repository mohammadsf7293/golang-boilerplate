package errval

import (
	"errors"
)

var (
	ErrInternal           = errors.New("internal server error")
	ErrNotFound           = errors.New("not found")
	ErrInvalidTaskType    = errors.New("invalid task type")
	ErrConcurrentUpdate   = errors.New("concurrent status update conflict")
	ErrAlreadyProcessed   = errors.New("message already processed")
)
