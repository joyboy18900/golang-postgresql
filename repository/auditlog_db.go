package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type auditLogRepositoryDB struct {
	pool *pgxpool.Pool
}

func NewAuditLogRepositoryDB(pool *pgxpool.Pool) AuditLogRepository {
	return auditLogRepositoryDB{pool: pool}
}

func (r auditLogRepositoryDB) Create(ctx context.Context, entry AuditLog) (*AuditLog, error) {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO audit_log (actor_id, action, entity_type, entity_id, metadata)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, actor_id, action, entity_type, entity_id, metadata, created_at`,
		entry.ActorID, entry.Action, entry.EntityType, entry.EntityID, entry.Metadata,
	)

	var created AuditLog
	if err := row.Scan(&created.ID, &created.ActorID, &created.Action, &created.EntityType,
		&created.EntityID, &created.Metadata, &created.CreatedAt); err != nil {
		return nil, fmt.Errorf("create audit log: %w", err)
	}

	return &created, nil
}

func (r auditLogRepositoryDB) ListByActor(ctx context.Context, actorID int64, limit int) ([]AuditLog, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, actor_id, action, entity_type, entity_id, metadata, created_at
		 FROM audit_log
		 WHERE actor_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		actorID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list audit log by actor: %w", err)
	}
	defer rows.Close()

	var entries []AuditLog
	for rows.Next() {
		var entry AuditLog
		if err := rows.Scan(&entry.ID, &entry.ActorID, &entry.Action, &entry.EntityType,
			&entry.EntityID, &entry.Metadata, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit log rows: %w", err)
	}

	return entries, nil
}

func (r auditLogRepositoryDB) CopyInsert(ctx context.Context, entries []AuditLog) (int64, error) {
	rows := make([][]any, len(entries))
	for i, entry := range entries {
		rows[i] = []any{entry.ActorID, entry.Action, entry.EntityType, entry.EntityID, entry.Metadata, entry.CreatedAt}
	}

	n, err := r.pool.CopyFrom(ctx,
		pgx.Identifier{"audit_log"},
		[]string{"actor_id", "action", "entity_type", "entity_id", "metadata", "created_at"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return 0, fmt.Errorf("copy insert audit log: %w", err)
	}

	return n, nil
}

func (r auditLogRepositoryDB) Analyze(ctx context.Context) error {
	if _, err := r.pool.Exec(ctx, "ANALYZE audit_log"); err != nil {
		return fmt.Errorf("analyze audit log: %w", err)
	}

	return nil
}

func (r auditLogRepositoryDB) ExplainListByActor(ctx context.Context, actorID int64, limit int) (string, error) {
	var plan string
	err := r.pool.QueryRow(ctx,
		`EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
		 SELECT id, actor_id, action, entity_type, entity_id, metadata, created_at
		 FROM audit_log
		 WHERE actor_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		actorID, limit,
	).Scan(&plan)
	if err != nil {
		return "", fmt.Errorf("explain list audit log by actor: %w", err)
	}

	return plan, nil
}
