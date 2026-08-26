# golang-postgresql

Postgres schema evolution and query performance, proven with real numbers:
versioned migrations, an indexing case study, and monthly table
partitioning with a working pruning demo. One entity, `audit_log`, carries
the whole story: an actor's activity history query that starts unindexed,
gets indexed, then gets partitioned by month.

## Run

### Option A: docker compose (full stack, already migrated and seeded)

```bash
docker compose up -d --build
docker compose logs seed --tail 5   # "seeded 1000000 rows total"
curl "http://localhost:8080/audit-log?actor_id=42"
```

Runs `migrate up` (through the partitioned schema), seeds 1,000,000 rows,
then starts the API on `:8080`. See `curl/flow.md` for requests. **Does not
reproduce the before/after numbers below** - the index and partitioning
already exist by the time `app` starts. Use option B for that.

### Option B: manual before/after reproduction

Requires a local Postgres reachable via `config.yaml` (or `docker compose up
-d postgres`).

```bash
go run . migrate goto 1     # table exists, no index - the "before" state
go run . seed 1000000       # ~1M rows, pgx CopyFrom, ANALYZE at the end
go run . bench <actor_id>   # capture "before" numbers (pick an actor_id from the seeded data)
go run . migrate goto 2     # add the index - the "after" state
go run . bench <actor_id>   # capture "after" numbers
go run . migrate goto 3     # convert to monthly partitions
```

`bench` prints a one-line summary plus the full `EXPLAIN (ANALYZE, BUFFERS,
FORMAT JSON)` output.

## Schema (`migrations/`)

1. **`0001_create_audit_log_table`** - plain, unindexed `audit_log`
   (`actor_id`, `action`, `entity_type`, `entity_id`, `metadata jsonb`,
   `created_at`). Deliberately the "before" state.
2. **`0002_add_actor_id_index`** - composite index on `(actor_id,
   created_at DESC)`, so it covers the `ORDER BY ... LIMIT` too.
3. **`0003_partition_audit_log_by_month`** - converts `audit_log` to a
   partitioned table. Postgres can't `ALTER TABLE ... PARTITION BY` an
   existing table, so this builds a new partitioned table, copies the
   data, rebuilds the index, then renames it into place.

## Benchmark Data

Query: `SELECT ... FROM audit_log WHERE actor_id = $1 ORDER BY created_at
DESC LIMIT 50`. Seeded with 1,000,000 rows across 20,000 actors.

**Indexing** (actor `854`, 80 matching rows):

| | plan | latency |
|---|---|---|
| before (`migrate goto 1`) | Seq Scan | 25.33 ms |
| after (`migrate goto 2`) | Bitmap Heap Scan | 0.43 ms |

~59x faster. Planner picks Bitmap Heap Scan over a plain Index Scan
because 80 scattered matching rows make batch-fetching cheaper than an
ordered index walk.

**Partition pruning** (`created_at`-range query on the partitioned
table): plan touches only `audit_log_y2026m08`, `Execution Time: 0.030
ms`.

| partition | rows |
|---|---|
| audit_log_y2026m05 | 251,958 |
| audit_log_y2026m06 | 244,375 |
| audit_log_y2026m07 | 251,591 |
| audit_log_y2026m08 | 252,077 |

## Key Technical Takeaways / Gotchas

- Partitioned by month on `created_at`: audit logs are append-only and
  queried by recency, so writes/range-reads land in one partition and old
  months drop with `DROP TABLE` instead of row-by-row `DELETE`.
- The actor-lookup query doesn't prune (`actor_id` isn't the partition
  key) - it still checks every partition. Partitioning and the index
  solve different problems; both are needed.
- Rows outside the four declared months land in `audit_log_default`;
  moving them into a real partition needs a manual `DETACH PARTITION` +
  backfill, no automatic rebalancing.
- Migration `0003` leaves the pre-partition table as
  `audit_log_unpartitioned` (needed for `down`) - rolling back restores
  that old snapshot, not current state.
- `migrate`/`seed`/`bench` stay CLI subcommands (`go run . <cmd>`), not
  endpoints - one-time proof steps, not things an API client should
  trigger.

## API

- `POST /audit-log` - create one entry
- `GET /audit-log?actor_id=X&limit=Y` - the case-study query (`limit`
  defaults to 50)

See `curl/flow.md` for examples.

## Not done on purpose

- No `pg_partman`/cron-based automatic future-partition creation, no
  `DEFAULT`-partition rebalancing tooling.
- No retry/backoff on seeding.
- No auth on the API (see `golang-fiber-jwt-auth`).

## Tests

```bash
go test ./...
```

- `bench_service_test.go` - parses `EXPLAIN (FORMAT JSON)` output.
- `seed_service_test.go` - batch splitting during seeding, mocked
  repository.
- `audit_log_integration_test.go` - migrations round-trip; plan is a Seq
  Scan before the index and not after; rows land in the right partition;
  range queries prune; both endpoints return the right envelope/errors.

The integration test owns its own database - run it against a scratch
Postgres, not the `docker compose` stack from option A, or it will wipe
that demo data.
