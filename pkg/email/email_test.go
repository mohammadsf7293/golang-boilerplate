package email

import (
	"testing"
	"time"
)

func TestSendEmailTask_Execute(t *testing.T) {
	task := SendEmailTask{}
	params := map[string]string{
		"to":      "user@example.com",
		"subject": "Test Email",
		"body":    "This is a test email.",
	}

	// Start measuring time
	start := time.Now()

	// Execute the task
	err := task.Execute(params)

	// Calculate elapsed time
	elapsed := time.Since(start)

	// Check if the function executed without errors
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check if the elapsed time is at least 3 seconds
	if elapsed < 3*time.Second {
		t.Fatalf("expected at least 3 seconds delay, got %v", elapsed)
	}
}
