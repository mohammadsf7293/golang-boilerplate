package process

import (
	"errors"
	"github.com/sf7293/task-manager/internal/domain"
	"github.com/sf7293/task-manager/pkg/email"
	"github.com/sf7293/task-manager/pkg/query"
	"math/rand"
	"time"
)

type Process interface {
	Execute(params map[string]string) error
}

func NewProcess(taskType domain.TaskType) (Process, error) {
	switch taskType {
	case domain.SendEmail:
		return email.NewSendEmailTask(), nil
	case domain.RunQuery:
		randomFunc := func() int {
			r := rand.New(rand.NewSource(time.Now().UnixNano()))
			return r.Intn(100) + 1
		}
		process := query.NewRunQueryTask(randomFunc)
		return process, nil
	default:
		return nil, errors.New("unrecognized task type")
	}
}
