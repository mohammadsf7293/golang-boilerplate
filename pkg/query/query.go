package query

import (
	"errors"
	"log/slog"
	"time"
)

type RunQueryTask struct {
	RandomFunc func() int
}

// NewRunQueryTask is a constructor that takes a random function as a dependency
func NewRunQueryTask(randomFunc func() int) RunQueryTask {
	return RunQueryTask{
		RandomFunc: randomFunc,
	}
}

func (q RunQueryTask) Execute(params map[string]string) (err error) {
	slog.Info("run_query parameters:", "params", params)
	time.Sleep(3 * time.Second)

	// q.Random func is an injected function which returns random number between 1 and 100
	randomNumber := q.RandomFunc()
	// This function fails for 20% of times
	if randomNumber <= 20 {
		slog.Warn("Error occurred while executing the query", "params", params)
		return errors.New("run_query failed")
	}

	return nil
}
