package domain

import (
	"context"
	"time"
)

type DistributedLock interface {
	Ping(ctx context.Context) (err error)
	Lock(lockKey string, lockTimeDuration time.Duration) (result bool, err error)
	Unlock(lockKey string) (err error)
	Close() error
}
