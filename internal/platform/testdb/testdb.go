// Package testdb creates disposable PostgreSQL databases for integration
// tests. It is intentionally opt-in: tests skip when the DB_* environment is
// absent, so `go test ./...` still works without a database. CI sets DB_* and
// therefore runs every SQL-backed test against a real PostgreSQL 16 server.
package testdb

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/necly/services/internal/platform/database"
	"gorm.io/gorm"
)

// Available reports whether a PostgreSQL test server is configured.
func Available() bool {
	return os.Getenv("DB_HOST") != "" && os.Getenv("DB_USER") != ""
}

// Open creates a dedicated database and returns a connection to it. The
// returned cleanup closes the connection and drops the database.
func Open() (*gorm.DB, func(), error) {
	cfg := database.Config{
		Host:     env("DB_HOST", "127.0.0.1"),
		Port:     envInt("DB_PORT", 5432),
		User:     os.Getenv("DB_USER"),
		Password: os.Getenv("DB_PASSWORD"),
		Name:     env("DB_NAME", "postgres"),
		SSLMode:  env("DB_SSL_MODE", "disable"),
	}
	dbName, err := databaseName()
	if err != nil {
		return nil, nil, err
	}

	maintenance, err := database.Open(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to maintenance database: %w", err)
	}
	maintenanceSQL, _ := maintenance.DB()
	if err := maintenance.Exec("CREATE DATABASE " + quoteIdentifier(dbName)).Error; err != nil {
		maintenanceSQL.Close()
		return nil, nil, fmt.Errorf("create test database: %w", err)
	}

	testConfig := cfg
	testConfig.Name = dbName
	db, err := database.Open(testConfig)
	if err != nil {
		_ = maintenance.Exec("DROP DATABASE IF EXISTS " + quoteIdentifier(dbName)).Error
		maintenanceSQL.Close()
		return nil, nil, fmt.Errorf("connect to test database: %w", err)
	}

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		// Give PostgreSQL a moment to release the connections before dropping.
		time.Sleep(25 * time.Millisecond)
		_ = maintenance.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ?", dbName).Error
		_ = maintenance.Exec("DROP DATABASE IF EXISTS " + quoteIdentifier(dbName)).Error
		maintenanceSQL.Close()
	}
	return db, cleanup, nil
}

func databaseName() (string, error) {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "necly_test_" + hex.EncodeToString(random), nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
