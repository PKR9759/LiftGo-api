// internal/middleware/ratelimit.go
package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// rateLimitConfig holds parsed env values so they are read once at startup.
type rateLimitConfig struct {
	max        int64
	windowSecs int64
}

func newRateLimitConfig() rateLimitConfig {
	cfg := rateLimitConfig{
		max:        100,
		windowSecs: 60,
	}

	if s := os.Getenv("RATE_LIMIT_MAX"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			cfg.max = n
		}
	}
	if s := os.Getenv("RATE_LIMIT_WINDOW_SECS"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			cfg.windowSecs = n
		}
	}

	return cfg
}

// RateLimit returns a Chi-compatible middleware that enforces a sliding window
// rate limit per client IP using Redis.
//
// If rdb is nil or USE_CACHE != "true", the middleware is a no-op and every
// request is allowed through — Redis being unavailable never blocks traffic.
func RateLimit(rdb *redis.Client) func(http.Handler) http.Handler {
	cfg := newRateLimitConfig()
	useCache := os.Getenv("USE_CACHE") == "true"

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bypass: caching disabled or Redis unavailable.
			if !useCache || rdb == nil {
				next.ServeHTTP(w, r)
				return
			}

			ip := extractIP(r)
			key := "ratelimit:" + ip
			ctx := r.Context()

			count, ttlSecs, err := incrementCounter(ctx, rdb, key, cfg.windowSecs)
			if err != nil {
				// Redis error → fail open (allow the request).
				slog.Warn("rate limiter Redis error — allowing request", "error", err, "ip", ip)
				next.ServeHTTP(w, r)
				return
			}

			remaining := cfg.max - count
			if remaining < 0 {
				remaining = 0
			}
			resetAt := time.Now().Unix() + ttlSecs

			// Set informational headers on every request (allowed or blocked).
			w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(cfg.max, 10))
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt, 10))

			if count > cfg.max {
				slog.Warn("rate limit exceeded",
					"ip", ip,
					"request_count", count,
					"limit", cfg.max,
					"path", r.URL.Path,
				)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", strconv.FormatInt(ttlSecs, 10))
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":                "rate limit exceeded",
					"retry_after_seconds":  ttlSecs,
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// incrementCounter uses a Redis pipeline to atomically INCR the counter and
// set an expiry only on the first request in the window.
// Returns the current count and remaining TTL in seconds.
func incrementCounter(ctx context.Context, rdb *redis.Client, key string, windowSecs int64) (int64, int64, error) {
	pipe := rdb.Pipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, time.Duration(windowSecs)*time.Second) // no-op after first call sets it
	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, windowSecs, err
	}

	count := incrCmd.Val()

	// Set expiry explicitly only when count == 1 (first hit in this window)
	// so we do not reset the window on every request.
	if count == 1 {
		rdb.Expire(ctx, key, time.Duration(windowSecs)*time.Second)
	}

	// Get remaining TTL for Retry-After and Reset headers.
	ttlDur, err := rdb.TTL(ctx, key).Result()
	if err != nil || ttlDur < 0 {
		return count, windowSecs, nil
	}

	return count, int64(ttlDur.Seconds()), nil
}

// extractIP returns the real client IP, checking X-Forwarded-For first
// (Render sits behind a proxy) then falling back to RemoteAddr.
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can be a comma-separated list; take the first entry.
		parts := strings.SplitN(xff, ",", 2)
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
