package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang-postgresql/handler"
	"golang-postgresql/logs"
	"golang-postgresql/repository"
	"golang-postgresql/service"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/viper"
)

func main() {
	initConfig()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run . <migrate|seed|bench|serve> [args]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "migrate":
		runMigrate(os.Args[2:])
	case "seed":
		runSeed(os.Args[2:])
	case "bench":
		runBench(os.Args[2:])
	case "serve":
		runServe()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(1)
	}
}

func initConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		panic(fmt.Errorf("read config: %w", err))
	}
}

func postgresDSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		viper.GetString("db.user"),
		viper.GetString("db.password"),
		viper.GetString("db.host"),
		viper.GetInt("db.port"),
		viper.GetString("db.name"),
		viper.GetString("db.sslmode"),
	)
}

func initPool() *pgxpool.Pool {
	pool, err := pgxpool.New(context.Background(), postgresDSN())
	if err != nil {
		panic(fmt.Errorf("open postgres pool: %w", err))
	}
	if err := pool.Ping(context.Background()); err != nil {
		panic(fmt.Errorf("ping postgres: %w", err))
	}

	return pool
}

func newMigrate() *migrate.Migrate {
	dsn := strings.Replace(postgresDSN(), "postgres://", "pgx5://", 1)

	m, err := migrate.New("file://migrations", dsn)
	if err != nil {
		panic(fmt.Errorf("new migrate: %w", err))
	}

	return m
}

func runMigrate(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down|goto N>")
		os.Exit(1)
	}

	m := newMigrate()
	defer m.Close()

	var err error
	switch args[0] {
	case "up":
		err = m.Up()
	case "down":
		err = m.Down()
	case "goto":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: migrate goto N")
			os.Exit(1)
		}
		var version int
		version, err = strconv.Atoi(args[1])
		if err == nil {
			err = m.Migrate(uint(version))
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown migrate subcommand %q\n", args[0])
		os.Exit(1)
	}

	if err != nil && err != migrate.ErrNoChange {
		panic(fmt.Errorf("migrate %s: %w", args[0], err))
	}

	logs.Info("migrate " + args[0] + " complete")
}

func runSeed(args []string) {
	rowCount := viper.GetInt("seed.row_count")
	if len(args) >= 1 {
		var err error
		rowCount, err = strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "row count must be an integer")
			os.Exit(1)
		}
	}
	if rowCount <= 0 {
		fmt.Fprintln(os.Stderr, "usage: seed [rowCount] (or set seed.row_count in config.yaml)")
		os.Exit(1)
	}

	pool := initPool()
	defer pool.Close()

	repo := repository.NewAuditLogRepositoryDB(pool)
	seedSvc := service.NewSeedService(repo, viper.GetInt("seed.actor_cardinality"), viper.GetInt("seed.batch_size"))

	inserted, err := seedSvc.Seed(context.Background(), rowCount)
	if err != nil {
		panic(fmt.Errorf("seed: %w", err))
	}

	logs.Info(fmt.Sprintf("seeded %d rows total", inserted))
}

func runBench(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: bench actorID")
		os.Exit(1)
	}
	actorID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Fprintln(os.Stderr, "actorID must be an integer")
		os.Exit(1)
	}

	pool := initPool()
	defer pool.Close()

	repo := repository.NewAuditLogRepositoryDB(pool)
	benchSvc := service.NewBenchService(repo)

	result, err := benchSvc.ListByActorPlan(context.Background(), actorID, 50)
	if err != nil {
		panic(fmt.Errorf("bench: %w", err))
	}

	fmt.Println(result.Summary)
	fmt.Println(result.RawPlan)
}

func runServe() {
	pool := initPool()
	defer pool.Close()

	auditLogRepo := repository.NewAuditLogRepositoryDB(pool)
	auditLogSvc := service.NewAuditLogService(auditLogRepo)
	auditLogHdlr := handler.NewAuditLogHandler(auditLogSvc)

	app := fiber.New()
	app.Post("/audit-log", auditLogHdlr.Create)
	app.Get("/audit-log", auditLogHdlr.ListByActor)

	port := viper.GetString("app.port")
	logs.Info("server started on port " + port)
	if err := app.Listen(":" + port); err != nil {
		logs.Error(err)
		os.Exit(1)
	}
}
