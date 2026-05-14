package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"
)

// DB represents a minimal interface satisfied by pgxpool.Pool and pgx.Tx.
type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// Log writes an audit trail entry recording entity modifications, state changes, and pricing adjustments.
func Log(ctx context.Context, db DB, entityType, entityID, actorID, action string, oldState, newState any) {
	var oldJSON, newJSON []byte
	if oldState != nil {
		oldJSON, _ = json.Marshal(oldState)
	}
	if newState != nil {
		newJSON, _ = json.Marshal(newState)
	}

	var actorVal *string
	if actorID != "" {
		actorVal = &actorID
	}

	_, err := db.Exec(ctx,
		`INSERT INTO audit_logs (entity_type, entity_id, actor_id, action, old_state, new_state)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		entityType, entityID, actorVal, action, oldJSON, newJSON,
	)
	if err != nil {
		slog.Error("failed to record audit log", "error", err, "entity_type", entityType, "entity_id", entityID, "action", action)
	}
}
