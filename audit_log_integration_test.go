package main_test

import (
	"context"
	"encoding/json"
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
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	cursorTestActorID = 7
	fixtureRowCount   = 2500
	tieRowCount       = 25
	pageLimit         = 20
	maxPageWalk       = 1000
)

const (
	testPostgresDSN = "postgres://postgres:postgres@localhost:5432/golang_postgresql?sslmode=disable"
	testMigrateDSN  = "pgx5://postgres:postgres@localhost:5432/golang_postgresql?sslmode=disable"
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

func connectTestGormDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(postgres.Open(testPostgresDSN), &gorm.Config{})
	if err != nil {
		t.Skipf("skipping integration test: open gorm db: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Skipf("skipping integration test: gorm db handle: %v", err)
	}
	if err := sqlDB.PingContext(context.Background()); err != nil {
		t.Skipf("skipping integration test: postgres not reachable: %v", err)
	}

	return db
}

func newTestMigrate(t *testing.T) *migrate.Migrate {
	t.Helper()

	m, err := migrate.New("file://migrations", testMigrateDSN)
	if err != nil {
		t.Fatalf("new migrate: %v", err)
	}

	return m
}

func migrateDownAll(t *testing.T, m *migrate.Migrate) {
	t.Helper()

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate down: %v", err)
	}
}

func TestAuditLogMigrations(t *testing.T) {
	pool := connectTestPool(t)
	defer pool.Close()

	m := newTestMigrate(t)
	defer m.Close()

	t.Cleanup(func() {
		m2, err := migrate.New("file://migrations", testMigrateDSN)
		if err != nil {
			return
		}
		defer m2.Close()
		_ = m2.Up()
	})

	migrateDownAll(t, m)

	ctx := context.Background()
	assertAuditLogTableExists(t, ctx, pool, false)

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	assertAuditLogTableExists(t, ctx, pool, true)

	migrateDownAll(t, m)
	assertAuditLogTableExists(t, ctx, pool, false)
}

func assertAuditLogTableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want bool) {
	t.Helper()

	var exists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('audit_log') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatalf("check audit_log existence: %v", err)
	}
	if exists != want {
		t.Errorf("audit_log exists = %v, want %v", exists, want)
	}
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
	repo.EXPECT().ListByActor(gomock.Any(), gomock.Any()).Return([]repository.AuditLog{created}, nil)

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
		Data service.ListAuditLogResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(envelope.Data.Items) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(envelope.Data.Items))
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

type auditLogFixtureRow struct {
	ActorID    int64
	Action     string
	EntityType string
	Metadata   []byte    `gorm:"type:jsonb"`
	CreatedAt  time.Time `gorm:"autoCreateTime:false"`
}

func (auditLogFixtureRow) TableName() string { return "audit_log" }

func TestAuditLogCursorPagination(t *testing.T) {
	db := connectTestGormDB(t)

	if err := db.Exec("DELETE FROM audit_log WHERE actor_id = ?", cursorTestActorID).Error; err != nil {
		t.Fatalf("reset fixture rows: %v", err)
	}

	base := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]auditLogFixtureRow, fixtureRowCount)
	for i := range rows {
		createdAt := base.Add(time.Duration(i) * time.Second)
		if i < tieRowCount {
			createdAt = base
		}
		rows[i] = auditLogFixtureRow{
			ActorID:    cursorTestActorID,
			Action:     "login",
			EntityType: "session",
			Metadata:   []byte("{}"),
			CreatedAt:  createdAt,
		}
	}
	if err := db.CreateInBatches(&rows, 500).Error; err != nil {
		t.Fatalf("seed fixture rows: %v", err)
	}

	var tieCount int64
	err := db.Raw("SELECT COUNT(*) FROM audit_log WHERE actor_id = ? AND created_at = ?", cursorTestActorID, base).
		Scan(&tieCount).Error
	if err != nil {
		t.Fatalf("count tie rows: %v", err)
	}
	if tieCount != tieRowCount {
		t.Fatalf("expected %d fixture rows sharing created_at %v, got %d - tiebreak is not exercised",
			tieRowCount, base, tieCount)
	}

	repo := repository.NewAuditLogRepositoryDB(db)
	svc := service.NewAuditLogService(repo)

	seen := make(map[int64]bool, fixtureRowCount)
	cursor := ""
	pages := 0
	for {
		if pages >= maxPageWalk {
			t.Fatalf("cursor walk did not terminate within %d pages", maxPageWalk)
		}
		pages++

		resp, err := svc.ListByActor(context.Background(), service.ListAuditLogRequest{
			ActorID: cursorTestActorID,
			Limit:   pageLimit,
			Cursor:  cursor,
		})
		if err != nil {
			t.Fatalf("list page %d: %v", pages, err)
		}

		for _, item := range resp.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate id %d on page %d", item.ID, pages)
			}
			seen[item.ID] = true
		}

		if resp.NextCursor == nil {
			break
		}
		cursor = *resp.NextCursor
	}

	if len(seen) != fixtureRowCount {
		t.Fatalf("walked %d distinct rows, want %d", len(seen), fixtureRowCount)
	}
}
