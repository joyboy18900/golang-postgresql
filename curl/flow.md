# Manual test flow

Walkthrough for exercising the audit log API by hand.

## Start

```bash
docker compose up -d --build
docker compose ps
docker compose logs app --tail 20   # should show "server started on port 8080"
```

## 1. Create an audit log entry

```bash
curl -X POST http://localhost:8080/audit-log \
  -H "Content-Type: application/json" \
  -d '{"actor_id":42,"action":"login","entity_type":"session"}'
```

```json
{ "code": 201, "message": "audit log entry created", "data": { "id": 1, "actor_id": 42, "action": "login", "entity_type": "session", "entity_id": null, "metadata": {}, "created_at": "2026-08-26T08:25:03.9061Z" } }
```

## 2. List an actor's activity history

```bash
curl "http://localhost:8080/audit-log?actor_id=42"
```

```json
{ "code": 200, "message": "audit log entries retrieved", "data": { "items": [ { "id": 1, "actor_id": 42, "action": "login", "entity_type": "session", "entity_id": null, "metadata": {}, "created_at": "2026-08-26T08:25:03.9061Z" } ], "next_cursor": null } }
```

`limit` defaults to 50 and can be overridden: `?actor_id=42&limit=10`.

## 3. Walk pages with the cursor

When a page is full, `next_cursor` is a non-null string. Pass it back as
`cursor` to fetch the next page; keep going until `next_cursor` is `null`.

```bash
curl "http://localhost:8080/audit-log?actor_id=42&limit=10"
curl "http://localhost:8080/audit-log?actor_id=42&limit=10&cursor=MTc4NzczMjcwMzkwNjEyMzo0Mg"
```

## 4. Rejection cases

Missing `actor_id`:

```bash
curl "http://localhost:8080/audit-log"
```

```json
{ "code": 422, "message": "actor_id query parameter is required", "data": null }
```

Malformed `cursor`:

```bash
curl "http://localhost:8080/audit-log?actor_id=42&cursor=not-a-cursor"
```

```json
{ "code": 422, "message": "invalid cursor", "data": null }
```

Missing required fields on create:

```bash
curl -X POST http://localhost:8080/audit-log \
  -H "Content-Type: application/json" \
  -d '{"actor_id":42}'
```

```json
{ "code": 422, "message": "actor_id, action and entity_type are required", "data": null }
```

## Stop

```bash
docker compose down -v
```
