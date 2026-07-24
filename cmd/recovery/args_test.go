package main

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseRecoveryArgs mirrors the CLI argument parsing used by cmd/recovery.
func parseRecoveryArgs(args []string) (status string, pastSeconds int64, limit int64, err error) {
	if len(args) < 4 {
		return "", 0, 0, assert.AnError
	}
	status = args[1]
	pastSeconds, err = strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return "", 0, 0, err
	}
	limit, err = strconv.ParseInt(args[3], 10, 64)
	if err != nil {
		return "", 0, 0, err
	}
	return status, pastSeconds, limit, nil
}

func TestParseRecoveryArgs(t *testing.T) {
	status, pastSeconds, limit, err := parseRecoveryArgs([]string{"queue_recovery", "queued", "30", "100"})
	require.NoError(t, err)
	assert.Equal(t, "queued", status)
	assert.Equal(t, int64(30), pastSeconds)
	assert.Equal(t, int64(100), limit)
}

func TestParseRecoveryArgsRejectsMissingLimit(t *testing.T) {
	_, _, _, err := parseRecoveryArgs([]string{"queue_recovery", "queued", "30"})
	require.Error(t, err)
}
