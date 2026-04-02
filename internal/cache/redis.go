// internal/cache/redis.go
package cache

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewClient initialises and returns a *redis.Client using REDIS_URL.
//
// Behaviour:
//   - If REDIS_URL is empty → warn and return (nil, nil) unless USE_CACHE=true, in which case it exits.
//   - If ping fails and USE_CACHE=true → log error and exit (cache is required).
//   - If ping fails and USE_CACHE!=true → log warning and return (nil, nil) so the rest of the app
//     continues without Redis.
//
// Callers must nil-check the returned client before using it.
func NewClient(ctx context.Context) (*redis.Client, error) {
	useCache := os.Getenv("USE_CACHE") == "true"
	redisURL := os.Getenv("REDIS_URL")

	if redisURL == "" {
		if useCache {
			slog.Error("USE_CACHE=true but REDIS_URL is not set — cannot start without Redis")
			os.Exit(1)
		}
		slog.Warn("REDIS_URL not set — Redis caching and rate limiting disabled")
		return nil, nil
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		if useCache {
			slog.Error("invalid REDIS_URL — cannot start without Redis", "error", err)
			os.Exit(1)
		}
		slog.Warn("invalid REDIS_URL — Redis disabled", "error", err)
		return nil, nil
	}

	client := redis.NewClient(opts)

	if _, err := client.Ping(ctx).Result(); err != nil {
		_ = client.Close()
		if useCache {
			slog.Error("Redis ping failed — USE_CACHE=true requires a working Redis connection", "error", err)
			os.Exit(1)
		}
		slog.Warn("Redis ping failed — caching and rate limiting disabled", "error", err)
		return nil, nil
	}

	slog.Info("Redis connected", "url", redisURL)
	return client, nil
}

// TTL returns the cache TTL from CACHE_TTL_SECS (default 60 s).
func TTL() time.Duration {
	if s := os.Getenv("CACHE_TTL_SECS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 60 * time.Second
}

// InvalidatePattern deletes all keys matching the glob pattern
// using SCAN + DEL so it is safe to run in production.
// Returns the number of keys deleted and any error encountered.
func InvalidatePattern(ctx context.Context, rdb *redis.Client, pattern string) (int64, error) {
	if rdb == nil {
		return 0, nil
	}

	var (
		cursor  uint64
		deleted int64
	)

	for {
		keys, nextCursor, err := rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return deleted, fmt.Errorf("redis SCAN error: %w", err)
		}

		if len(keys) > 0 {
			n, err := rdb.Del(ctx, keys...).Result()
			if err != nil {
				return deleted, fmt.Errorf("redis DEL error: %w", err)
			}
			deleted += n
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return deleted, nil
}
