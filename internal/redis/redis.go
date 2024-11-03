package redis

import (
	"context"
	redis "github.com/redis/go-redis/v9"
	"time"
)

type Client struct {
	Context     context.Context
	RedisClient *redis.Client
}

func NewClient(ctx context.Context, dsn string) (*Client, error) {
	opts, err := redis.ParseURL(dsn)
	if err != nil {
		panic(err)
	}

	redisClient := redis.NewClient(opts)
	return &Client{
		Context:     ctx,
		RedisClient: redisClient,
	}, nil
}

func (c *Client) Lock(lockKey string, lockTimeDuration time.Duration) (result bool, err error) {
	result, err = c.RedisClient.SetNX(c.Context, lockKey, 1, lockTimeDuration).Result()
	if err != nil {
		return false, err
	}

	return result, nil
}

func (c *Client) Unlock(lockKey string) (err error) {
	err = c.RedisClient.Del(c.Context, lockKey).Err()
	return err
}

func (c *Client) Close() (err error) {
	err = c.RedisClient.Close()
	return err
}

func (c *Client) Ping(ctx context.Context) (err error) {
	err = c.RedisClient.Ping(ctx).Err()
	return err
}
