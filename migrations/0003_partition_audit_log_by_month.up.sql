CREATE TABLE audit_log_partitioned (
    id          BIGSERIAL NOT NULL,
    actor_id    BIGINT NOT NULL,
    action      VARCHAR(64) NOT NULL,
    entity_type VARCHAR(64) NOT NULL,
    entity_id   BIGINT,
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE audit_log_y2026m05 PARTITION OF audit_log_partitioned FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE audit_log_y2026m06 PARTITION OF audit_log_partitioned FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
CREATE TABLE audit_log_y2026m07 PARTITION OF audit_log_partitioned FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
CREATE TABLE audit_log_y2026m08 PARTITION OF audit_log_partitioned FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
CREATE TABLE audit_log_default PARTITION OF audit_log_partitioned DEFAULT;

INSERT INTO audit_log_partitioned (id, actor_id, action, entity_type, entity_id, metadata, created_at)
SELECT id, actor_id, action, entity_type, entity_id, metadata, created_at FROM audit_log;

SELECT setval(
    pg_get_serial_sequence('audit_log_partitioned', 'id'),
    COALESCE((SELECT MAX(id) FROM audit_log_partitioned), 0) + 1,
    false
);

CREATE INDEX idx_audit_log_partitioned_actor_id_created_at ON audit_log_partitioned (actor_id, created_at DESC);

ALTER TABLE audit_log RENAME TO audit_log_unpartitioned;
ALTER TABLE audit_log_partitioned RENAME TO audit_log;
