package db

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type tracerSQLKey struct{}

type QueryTracer struct{}

func (t *QueryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, tracerSQLKey{}, data.SQL)
}

func (t *QueryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	reqID, _ := ctx.Value(customMiddleware.RequestIDKey).(string)
	if reqID == "" {
		return
	}
	sqlStr, _ := ctx.Value(tracerSQLKey{}).(string)
	if data.Err != nil {
		slog.Error("db query failed", "request_id", reqID, "sql", sqlStr, "error", data.Err)
	} else {
		slog.Debug("db query executed", "request_id", reqID, "sql", sqlStr, "rows_affected", data.CommandTag.RowsAffected())
	}
}

func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("unable to parse database config: %w", err)
	}

	config.ConnConfig.Tracer = &QueryTracer{}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("unable to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("unable to reach database: %w", err)
	}

	return pool, nil
}

func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	pattern := filepath.Join("migrations", "*.sql")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("reading migrations: %w", err)
	}

	sort.Strings(files)

	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("reading %s: %w", f, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("running %s: %w", f, err)
		}
		slog.Info("migrated", "file", f)
	}

	return nil
}
