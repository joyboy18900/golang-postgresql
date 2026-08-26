package main

import (
	"fmt"
	"os"
	"strings"

	"golang-postgresql/handler"
	"golang-postgresql/logs"
	"golang-postgresql/repository"
	"golang-postgresql/service"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/spf13/viper"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	initConfig()
	runMigrations()

	db := openGormDB()

	auditLogRepo := repository.NewAuditLogRepositoryDB(db)
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

func openGormDB() *gorm.DB {
	db, err := gorm.Open(postgres.Open(postgresDSN()), &gorm.Config{})
	if err != nil {
		panic(fmt.Errorf("open postgres: %w", err))
	}

	return db
}

func runMigrations() {
	dsn := strings.Replace(postgresDSN(), "postgres://", "pgx5://", 1)

	m, err := migrate.New("file://migrations", dsn)
	if err != nil {
		panic(fmt.Errorf("new migrate: %w", err))
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		panic(fmt.Errorf("migrate up: %w", err))
	}

	logs.Info("migrations up to date")
}
