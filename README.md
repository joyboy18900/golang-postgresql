# golang-postgresql

Fiber + GORM + Postgres reference: one entity, `audit_log`, with versioned
migrations and cursor-based (keyset) pagination on its actor-activity
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
- `GET /audit-log?actor_id=X&limit=Y&cursor=Z`

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
   index alone - correctness is unaffected, since the `id DESC` tiebreak
   is enforced by the query itself.

## Pagination

`GET /audit-log` uses keyset pagination, not offset. A cursor is an opaque
base64 string encoding `created_at` (microseconds) and `id` of the last
row on the previous page. The service fetches one extra row past `limit`
to decide whether a next page exists: if it gets `limit+1` rows back, it
trims to `limit` and returns `next_cursor` built from the last kept row;
otherwise `next_cursor` is `null` and the walk is over.

```json
{ "code": 200, "message": "audit log entries retrieved",
  "data": { "items": [ ... ], "next_cursor": "MTc4NzczMjcwMzkwNjEyMzo0Mg" } }
```

Pass that `next_cursor` back as `?cursor=` to fetch the following page.

## Tests

```bash
go test ./...
go generate ./...   # regenerate repository mocks
```

- `service/cursor_test.go` - cursor encode/decode round trip, malformed
  and garbage-but-valid-base64 rejection.
- `audit_log_integration_test.go` - migration round trip; a full cursor
  walk over a few thousand fixture rows (including a forced tie on
  `created_at` to exercise the `id DESC` tiebreak) that must land every
  row exactly once with no gaps or duplicates; both endpoints' envelope
  and validation errors.

The integration tests own the database - run them against a scratch
Postgres, not one holding data you care about.
