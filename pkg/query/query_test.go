package query

import (
	"testing"
)

// TestRunQueryTask_Execute_Success: Checking success when random number is greater than 20
func TestRunQueryTask_Execute_Success(t *testing.T) {
	// Mock the RandomFunc to always return a number greater than 20
	task := NewRunQueryTask(func() int {
		return 21
	})

	params := map[string]string{
		"query": "SELECT * FROM users",
	}

	err := task.Execute(params)

	// Check if the function executed without errors
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

// TestRunQueryTask_Execute_Failure1: Testing failure while random number is exactly 20: Marginal test case
func TestRunQueryTask_Execute_Failure1(t *testing.T) {
	// Mock the RandomFunc to always return a number less than or equal to 20
	task := NewRunQueryTask(func() int {
		return 20
	})

	params := map[string]string{
		"query": "SELECT * FROM users",
	}

	err := task.Execute(params)
	// Check if the function returned an error
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
}

// TestRunQueryTask_Execute_Failure2: Testing failure while random number is less than 20
func TestRunQueryTask_Execute_Failure2(t *testing.T) {
	// Mock the RandomFunc to always return a number less than or equal to 20
	task := NewRunQueryTask(func() int {
		return 19
	})

	params := map[string]string{
		"query": "SELECT * FROM users",
	}

	err := task.Execute(params)
	// Check if the function returned an error
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
}
