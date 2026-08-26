package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang-postgresql/handler"
	"golang-postgresql/mock/mock_repository"
	"golang-postgresql/repository"
	"golang-postgresql/service"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/mock/gomock"
)

const (
	testPostgresDSN = "postgres://postgres:postgres@localhost:5432/golang_postgresql?sslmode=disable"
	testMigrateDSN  = "pgx5://postgres:postgres@localhost:5432/golang_postgresql?sslmode=disable"
	testActorCount  = 1000
	testRowCount    = 50000
	testBatchSize   = 10000
	targetActorID   = 1
)

func connectTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), testPostgresDSN)
	if err != nil {
		t.Skipf("skipping integration test: open pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("skipping integration test: postgres not reachable: %v", err)
	}

	return pool
}

func newTestMigrate(t *testing.T) *migrate.Migrate {
	t.Helper()

	m, err := migrate.New("file://migrations", testMigrateDSN)
	if err != nil {
		t.Fatalf("new migrate: %v", err)
	}

	return m
}

func migrateTo(t *testing.T, m *migrate.Migrate, version uint) {
	t.Helper()

	if err := m.Migrate(version); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate to %d: %v", version, err)
	}
}

func migrateDownAll(t *testing.T, m *migrate.Migrate) {
	t.Helper()

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate down: %v", err)
	}
}

func TestAuditLogMigrationsAndQueryPlan(t *testing.T) {
	pool := connectTestPool(t)
	defer pool.Close()

	m := newTestMigrate(t)
	defer m.Close()

	migrateDownAll(t, m)
	t.Cleanup(func() {
		m2, err := migrate.New("file://migrations", testMigrateDSN)
		if err != nil {
			return
		}
		defer m2.Close()
		_ = m2.Down()
	})

	migrateTo(t, m, 1)

	ctx := context.Background()
	repo := repository.NewAuditLogRepositoryDB(pool)
	seeder := service.NewSeedService(repo, testActorCount, testBatchSize)

	if _, err := seeder.Seed(ctx, testRowCount); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("seq scan before index", func(t *testing.T) {
		rawPlan, err := repo.ExplainListByActor(ctx, targetActorID, 50)
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		if !strings.Contains(rawPlan, "Seq Scan") {
			t.Errorf("expected plan to contain a Seq Scan node, got: %s", rawPlan)
		}
	})

	migrateTo(t, m, 2)
	if err := repo.Analyze(ctx); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	t.Run("index scan after index", func(t *testing.T) {
		rawPlan, err := repo.ExplainListByActor(ctx, targetActorID, 50)
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		if strings.Contains(rawPlan, `"Node Type": "Seq Scan"`) {
			t.Errorf("expected the index to be used instead of a Seq Scan, got: %s", rawPlan)
		}
		if !strings.Contains(rawPlan, "Index Scan") {
			t.Errorf("expected plan to use the new index (Index Scan or Bitmap Index Scan), got: %s", rawPlan)
		}
	})

	migrateTo(t, m, 3)
	if err := repo.Analyze(ctx); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	t.Run("partition pruning on created_at range", func(t *testing.T) {
		var rawPlan string
		err := pool.QueryRow(ctx,
			`EXPLAIN (FORMAT JSON) SELECT * FROM audit_log
			 WHERE created_at >= '2026-08-01' AND created_at < '2026-09-01' LIMIT 50`,
		).Scan(&rawPlan)
		if err != nil {
			t.Fatalf("explain range query: %v", err)
		}

		for _, other := range []string{"audit_log_y2026m05", "audit_log_y2026m06", "audit_log_y2026m07", "audit_log_default"} {
			if strings.Contains(rawPlan, other) {
				t.Errorf("expected only audit_log_y2026m08 to be scanned, but plan mentions %s: %s", other, rawPlan)
			}
		}
		if !strings.Contains(rawPlan, "audit_log_y2026m08") {
			t.Errorf("expected plan to mention audit_log_y2026m08, got: %s", rawPlan)
		}
	})

	t.Run("row visible directly in its month partition", func(t *testing.T) {
		var id int64
		err := pool.QueryRow(ctx,
			`INSERT INTO audit_log (actor_id, action, entity_type, metadata, created_at)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			targetActorID, "create", "order", map[string]any{}, "2026-06-15T12:00:00Z",
		).Scan(&id)
		if err != nil {
			t.Fatalf("insert row for partition check: %v", err)
		}

		const partition = "audit_log_y2026m06"
		var found bool
		err = pool.QueryRow(ctx,
			fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1)", partition),
			id,
		).Scan(&found)
		if err != nil {
			t.Fatalf("query partition %s: %v", partition, err)
		}
		if !found {
			t.Errorf("expected row %d to be visible in partition %s", id, partition)
		}
	})

	migrateDownAll(t, m)

	t.Run("migrations round trip leaves no audit_log table", func(t *testing.T) {
		var exists bool
		err := pool.QueryRow(ctx, "SELECT to_regclass('audit_log') IS NOT NULL").Scan(&exists)
		if err != nil {
			t.Fatalf("check audit_log existence: %v", err)
		}
		if exists {
			t.Error("expected audit_log to not exist after migrating all the way down")
		}
	})
}

func newHandlerTestApp(repo repository.AuditLogRepository) *fiber.App {
	svc := service.NewAuditLogService(repo)
	hdlr := handler.NewAuditLogHandler(svc)

	app := fiber.New()
	app.Post("/audit-log", hdlr.Create)
	app.Get("/audit-log", hdlr.ListByActor)

	return app
}

func TestAuditLogHandler_CreateAndList(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_repository.NewMockAuditLogRepository(ctrl)

	created := repository.AuditLog{
		ID: 1, ActorID: 42, Action: "login", EntityType: "session",
		Metadata: map[string]any{}, CreatedAt: time.Now(),
	}
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(&created, nil)
	repo.EXPECT().ListByActor(gomock.Any(), int64(42), gomock.Any()).Return([]repository.AuditLog{created}, nil)

	app := newHandlerTestApp(repo)

	createReq := httptest.NewRequest(fiber.MethodPost, "/audit-log", strings.NewReader(
		`{"actor_id":42,"action":"login","entity_type":"session"}`,
	))
	createReq.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(createReq)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("create status = %d, want %d", resp.StatusCode, fiber.StatusCreated)
	}

	listReq := httptest.NewRequest(fiber.MethodGet, "/audit-log?actor_id=42", nil)

	resp, err = app.Test(listReq)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("list status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	var envelope struct {
		Data []service.AuditLogResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(envelope.Data))
	}
}

func TestAuditLogHandler_ListRequiresActorID(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_repository.NewMockAuditLogRepository(ctrl)
	app := newHandlerTestApp(repo)

	req := httptest.NewRequest(fiber.MethodGet, "/audit-log", nil)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	if resp.StatusCode != fiber.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnprocessableEntity)
	}
}
