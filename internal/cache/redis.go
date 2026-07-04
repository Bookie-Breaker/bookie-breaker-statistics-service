package cache

import (
	"context"
	"log/slog"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

func NewClient(ctx context.Context, redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opts)

	if err := redisotel.InstrumentTracing(client); err != nil {
		return nil, err
	}
	if err := redisotel.InstrumentMetrics(client); err != nil {
		return nil, err
	}

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	slog.Info("connected to Redis", "addr", opts.Addr)
	return client, nil
}
