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
2. **`0002_add_actor_id_index`** - `CREATE INDEX ... ON audit_log (actor_id,
   created_at DESC)`. Composite, not just `actor_id`, so it also covers the
   `ORDER BY ... LIMIT` for free. A real deployment would use `CREATE INDEX
   CONCURRENTLY` to avoid locking writers; skipped here since golang-migrate
   runs migrations in a transaction and this demo has no concurrent
   writers.
3. **`0003_partition_audit_log_by_month`** - converts `audit_log` to a
   partitioned table. Postgres can't `ALTER TABLE ... PARTITION BY` an
   existing table, and the partition key must be in the primary key. This
   migration builds a new partitioned table, copies the data, rebuilds the
   index, then renames it into place.

## Indexing case study

Query: `SELECT ... FROM audit_log WHERE actor_id = $1 ORDER BY created_at
DESC LIMIT 50`, a user's activity history lookup. Seeded with 1,000,000
rows across 20,000 actors (actor `854` below has 80 matching rows, a
realistic selectivity).

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

**~59x faster** (25.33 ms -> 0.43 ms). The planner picked a Bitmap Heap
Scan over a plain Index Scan because 80 scattered matching rows make
batch-fetching cheaper than an ordered index walk. Both use the new index;
`bench` reads the index name off the nested `Bitmap Index Scan` node to
report it correctly.

## Partitioning

Partitioned by month on `created_at`, four explicit partitions
(`audit_log_y2026m05` .. `m08`) bracketing the seeded data's date range,
plus an `audit_log_default` catch-all.

**Why `created_at` and monthly**: audit logs are append-only and almost
always queried by recency. Partitioning on the column every write and most
reads touch means inserts and range reads land in one partition, and old
months drop with `DROP TABLE` instead of a row-by-row `DELETE`.

**Proof of pruning**: a `created_at`-range query touches only the matching
partition.

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

The plan mentions only `audit_log_y2026m08`; `m05`, `m06`, `m07`, and
`default` never appear. Row counts show an even spread, nothing fell into
the default partition:

| partition | rows |
|---|---|
| audit_log_y2026m05 | 251,958 |
| audit_log_y2026m06 | 244,375 |
| audit_log_y2026m07 | 251,591 |
| audit_log_y2026m08 | 252,077 |

**Tradeoffs**: the actor-lookup query doesn't prune, since `actor_id` isn't
the partition key; it still checks every partition (via each partition's
own index copy). Partitioning and the index solve different problems, and
both are needed. Rows outside the four declared months land in
`audit_log_default`; moving them into a real partition later needs a manual
`DETACH PARTITION` plus backfill, with no automatic rebalancing. A
production setup would add `pg_partman` or a cron job to pre-create next
month's partition; out of scope here.

Migration `0003` also leaves the pre-partition table behind as
`audit_log_unpartitioned`. `up` never drops it, because `down` needs it to
roll back. Both copies sit on disk at once, and rolling `0003` back
restores that old snapshot, not current state - rows written after
partitioning aren't in it and are lost from a rollback's point of view.

## API

Two endpoints, standard envelope (`{code, message, data}`):

- `POST /audit-log` - create one entry
- `GET /audit-log?actor_id=X&limit=Y` - the case-study query (`limit`
  defaults to 50)

See `curl/flow.md` for examples.

`migrate`, `seed`, and `bench` stay CLI subcommands (`go run . <cmd>`), not
endpoints - they're one-time proof steps for this README, not things an API
client should trigger.

## Tests

```bash
go test ./...
```

- `service/bench_service_test.go` - parses `EXPLAIN (FORMAT JSON)` output,
  including the Bitmap Heap Scan / parallel Seq Scan shapes Postgres
  actually returns at this scale.
- `service/seed_service_test.go` - batch splitting during seeding, with a
  mocked repository.
- `audit_log_integration_test.go` - against a real Postgres: migrations
  round-trip cleanly; the actor query's plan is a Seq Scan before the index
  and not after; a row lands in the right month's partition; a
  `created_at`-range query prunes to one partition; both endpoints return
  the right envelope and validation errors.

The integration test owns the database it connects to: it drops the
schema, seeds its own data, and migrates back down when done. Run it
against a scratch Postgres, not the `docker compose` stack from option A,
or it will wipe that demo data.

## Not done on purpose

No `pg_partman`/cron-based automatic future-partition creation, no
`DEFAULT`-partition rebalancing tooling, no retry/backoff on seeding, no
auth on the API (out of scope for this project - see
`golang-fiber-jwt-auth`).
