package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type auditLogRow struct {
	ID         int64
	ActorID    int64
	Action     string
	EntityType string
	EntityID   *int64
	Metadata   []byte `gorm:"type:jsonb"`
	CreatedAt  time.Time
}

func (auditLogRow) TableName() string {
	return "audit_log"
}

type auditLogRepositoryDB struct {
	db *gorm.DB
}

func NewAuditLogRepositoryDB(db *gorm.DB) AuditLogRepository {
	return auditLogRepositoryDB{db: db}
}

func (r auditLogRepositoryDB) Create(ctx context.Context, entry AuditLog) (*AuditLog, error) {
	row, err := toRow(entry)
	if err != nil {
		return nil, fmt.Errorf("create audit log: %w", err)
	}

	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, fmt.Errorf("create audit log: %w", err)
	}

	created, err := toDomain(row)
	if err != nil {
		return nil, fmt.Errorf("create audit log: %w", err)
	}

	return &created, nil
}

func (r auditLogRepositoryDB) ListByActor(ctx context.Context, actorID int64, limit int) ([]AuditLog, error) {
	var rows []auditLogRow
	err := r.db.WithContext(ctx).
		Where("actor_id = ?", actorID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list audit log by actor: %w", err)
	}

	entries := make([]AuditLog, len(rows))
	for i, row := range rows {
		entry, err := toDomain(row)
		if err != nil {
			return nil, fmt.Errorf("list audit log by actor: %w", err)
		}
		entries[i] = entry
	}

	return entries, nil
}

func toRow(entry AuditLog) (auditLogRow, error) {
	metadata := entry.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}

	raw, err := json.Marshal(metadata)
	if err != nil {
		return auditLogRow{}, fmt.Errorf("marshal metadata: %w", err)
	}

	return auditLogRow{
		ID:         entry.ID,
		ActorID:    entry.ActorID,
		Action:     entry.Action,
		EntityType: entry.EntityType,
		EntityID:   entry.EntityID,
		Metadata:   raw,
		CreatedAt:  entry.CreatedAt,
	}, nil
}

func toDomain(row auditLogRow) (AuditLog, error) {
	metadata := map[string]any{}
	if len(row.Metadata) > 0 {
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
			return AuditLog{}, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	return AuditLog{
		ID:         row.ID,
		ActorID:    row.ActorID,
		Action:     row.Action,
		EntityType: row.EntityType,
		EntityID:   row.EntityID,
		Metadata:   metadata,
		CreatedAt:  row.CreatedAt,
	}, nil
}
