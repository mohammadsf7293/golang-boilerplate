package email

import (
	"log/slog"
	"time"
)

type SendEmailTask struct{}

func NewSendEmailTask() SendEmailTask {
	return SendEmailTask{}
}

func (e SendEmailTask) Execute(params map[string]string) error {
	slog.Info("send_email parameters:", "params", params)
	time.Sleep(3 * time.Second)
	return nil
}
