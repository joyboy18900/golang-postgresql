# golang-postgresql

Postgres schema evolution and query performance, proven with real numbers:
versioned migrations, an indexing case study (unindexed vs indexed
`EXPLAIN ANALYZE`), and monthly table partitioning with a working pruning
demo. One entity - `audit_log` - carries the whole story: a realistic
"actor's activity history" query that starts unindexed, then gets indexed,
then the table gets partitioned by month.

## Run

### Option A: docker compose (full stack, already migrated and seeded)

```bash
docker compose up -d --build
docker compose logs seed --tail 5   # "seeded 1000000 rows total"
curl "http://localhost:8080/audit-log?actor_id=42"
```

This runs `migrate up` (all three migrations, ending on the partitioned
schema), seeds 1,000,000 rows, then starts the API on `:8080`. See
`curl/flow.md` for the full request walkthrough. **This does not reproduce
the before/after indexing numbers below** - by the time `app` starts, the
index and partitioning already exist. Use option B for that.

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
2. **`0002_add_actor_id_index`** - `CREATE INDEX ... ON audit_log (actor_id,
   created_at DESC)`. Composite, not `actor_id` alone, so it also covers the
   `ORDER BY created_at DESC LIMIT` for free. A real deployment would use
   `CREATE INDEX CONCURRENTLY` to avoid locking writers; skipped here because
   golang-migrate runs each migration in a transaction and this demo has no
   concurrent writers during migration.
3. **`0003_partition_audit_log_by_month`** - converts `audit_log` to a
   partitioned table. Postgres can't `ALTER TABLE ... PARTITION BY` an
   existing table, and declarative partitioning requires the partition key
   in the primary key, so this migration builds a new partitioned table,
   copies the data across, rebuilds the index, and renames the new table
   into place of the old one.

## Indexing case study

Query: `SELECT ... FROM audit_log WHERE actor_id = $1 ORDER BY created_at
DESC LIMIT 50` - a realistic "show this user's activity history" lookup.
Seeded with 1,000,000 rows across 20,000 actors (actor `854` used below, 80
matching rows - about the selectivity a real actor lookup would see).

**Before** (`migrate goto 1`, no index):

```
Seq Scan on audit_log, Execution Time: 25.33 ms
```

The planner used two parallel workers and still scanned all 1,000,000 rows,
filtering out 333,307 non-matching rows per worker.

**After** (`migrate goto 2`, composite index added):

```
Bitmap Heap Scan using idx_audit_log_actor_id_created_at on audit_log, Execution Time: 0.43 ms
```

**~59x faster** (25.33 ms -> 0.43 ms). The planner chose a Bitmap Heap
Scan over a plain Index Scan because 80 matching rows scattered across the
table make a bitmap-then-fetch cheaper than an ordered index walk - both use
the new index; the bitmap path just batches the heap fetches. `go run .
bench` reports this correctly by reading the index name off the nested
`Bitmap Index Scan` node.

## Partitioning

Partitioned by month on `created_at`, four explicit partitions
(`audit_log_y2026m05` .. `m08`) bracketing the seeded data's date range,
plus an `audit_log_default` catch-all.

**Why `created_at` and monthly**: audit logs are append-only and almost
always queried by recency ("show me last month's activity" / retention
sweeps that drop old months). Partitioning on the column every write and
most reads touch means both inserts and range-scoped reads land in one
partition, and old months can be dropped with `DROP TABLE` instead of a
row-by-row `DELETE`.

**Proof of pruning** - a `created_at`-range query only touches the matching
partition, not all of them:

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT * FROM audit_log
WHERE created_at >= '2026-08-01' AND created_at < '2026-09-01' LIMIT 50;
```

```
Seq Scan on audit_log_y2026m08 audit_log
Buffers: shared hit=1
Execution Time: 0.030 ms
```

The plan mentions only `audit_log_y2026m08` - `m05`, `m06`, `m07`, and
`default` never appear. Row counts confirm an even spread with nothing
falling through to the default partition:

| partition | rows |
|---|---|
| audit_log_y2026m05 | 251,958 |
| audit_log_y2026m06 | 244,375 |
| audit_log_y2026m07 | 251,591 |
| audit_log_y2026m08 | 252,077 |

**Tradeoffs**: the actor-lookup query above (filtering on `actor_id`, not
`created_at`) does *not* prune - `actor_id` isn't the partition key, so that
query still checks every partition (using each partition's own copy of the
index). Partitioning and the `actor_id` index solve different problems and
both are needed. Rows outside the four declared months land in
`audit_log_default`; promoting them into a proper monthly partition later
needs a manual `ALTER TABLE ... DETACH PARTITION` plus backfill - there's no
automatic rebalancing. A production setup would add `pg_partman` or a cron
job to pre-create next month's partition ahead of time; both are out of
scope here.

## API

Two endpoints, standard envelope (`{code, message, data}`):

- `POST /audit-log` - create one entry
- `GET /audit-log?actor_id=X&limit=Y` - the case-study query itself
  (`limit` defaults to 50)

See `curl/flow.md` for full request/response examples.

`migrate`, `seed`, and `bench` are CLI subcommands (`go run . <cmd>`), not
HTTP endpoints - seeding a million rows and running `EXPLAIN ANALYZE` are
one-time proof steps for this README, not things an API client should be
able to trigger.

## Tests

```bash
go test ./...
```

- `service/bench_service_test.go` - unit tests for parsing `EXPLAIN (FORMAT
  JSON)` output, including the Bitmap Heap Scan / parallel Seq Scan shapes
  real Postgres actually returns at this data size.
- `service/seed_service_test.go` - unit test for batch splitting during
  seeding, with a mocked repository.
- `audit_log_integration_test.go` - against a real Postgres: migrating
  through all three versions and back down round-trips cleanly; the actor
  query's plan contains a Seq Scan before the index and no longer does
  after; a row lands in the correct month's partition table; a
  `created_at`-range query prunes to one partition; the two HTTP endpoints
  return the right envelope and validation errors.

## Not done on purpose

No `pg_partman`/cron-based automatic future-partition creation, no
`DEFAULT`-partition rebalancing tooling, no retry/backoff on seeding, no
auth on the API (out of scope for this project - see
`golang-fiber-jwt-auth`).
