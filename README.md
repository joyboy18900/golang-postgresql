# golang-postgresql

Fiber + GORM + Postgres reference: one entity, `audit_log`, with versioned
migrations and offset (page-number) pagination on its actor-activity
listing query.

## Run

```bash
docker compose up -d --build
curl -X POST http://localhost:8080/audit-log \
  -H "Content-Type: application/json" \
  -d '{"actor_id":42,"action":"login","entity_type":"session"}'
curl "http://localhost:8080/audit-log?actor_id=42"
```

The app runs pending migrations on startup, then serves on `:8080`. See
`curl/flow.md` for a full walkthrough.

## Endpoints

- `POST /audit-log`
- `GET /audit-log?actor_id=X` (`page`, `limit` optional)

See `curl/flow.md` for full request/response examples.

## Schema (`migrations/`)

1. **`0001_create_audit_log_table`** - `audit_log` (`actor_id`, `action`,
   `entity_type`, `entity_id`, `metadata jsonb`, `created_at`). The
   `created_at` column keeps its SQL-level `DEFAULT now()` as a safety net
   for any row inserted outside the app; the app itself always sets
   `created_at` client-side through GORM's `autoCreateTime` convention, so
   that default normally never fires.
2. **`0002_add_actor_id_index`** - composite index on `(actor_id,
   created_at DESC)`, covering the listing query's `WHERE` and `ORDER BY`.
   The index does not include `id`, so rows sharing the exact same
   `created_at` are ordered by a small in-memory sort rather than the
   index alone. Correctness is unaffected: the query's `id DESC` tiebreak
   still produces a strict order.

## Tests

```bash
go test ./...
go generate ./...   # regenerate repository and service mocks
```

- `service/auditlog_service_test.go` - the ceiling-division `total_pages`
  math and the `actor_id`/`page` validation guards.
- `audit_log_integration_test.go` - migration round trip; a full offset
  pagination walk over a few thousand fixture rows (including a forced tie
  on `created_at` to exercise the `id DESC` tiebreak) that must land every
  row exactly once with no gaps or duplicates, plus a past-the-last-page
  request; both endpoints' envelope and validation errors.

The integration tests own the database. Run them against a scratch
Postgres, not one holding data you care about.
