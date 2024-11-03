package errval

import (
	"errors"
)

var (
	ErrInternal        = errors.New("internal server error")
	ErrNotFound        = errors.New("not found")
	ErrInvalidTaskType = errors.New("invalid task type")
)
