package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/necly/services/internal/config"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/database"
)

func main() {
	var (
		host     = flag.String("host", "", "Database host")
		port     = flag.Int("port", 0, "Database port")
		user     = flag.String("user", "", "Database user")
		password = flag.String("password", "", "Database password")
		name     = flag.String("name", "", "Database name")
		sslmode  = flag.String("sslmode", "", "SSL mode")
		dir      = flag.String("dir", "migrations", "Directory containing versioned .sql files")
		help     = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	if *help {
		flag.PrintDefaults()
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	dbCfg := database.Config{
		Host:            getOrDefault(*host, cfg.Database.Host),
		Port:            getOrDefaultInt(*port, cfg.Database.Port),
		User:            getOrDefault(*user, cfg.Database.User),
		Password:        getOrDefault(*password, cfg.Database.Password),
		Name:            getOrDefault(*name, cfg.Database.Name),
		SSLMode:         getOrDefault(*sslmode, cfg.Database.SSLMode),
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Database.ConnMaxIdleTime,
		LogLevel:        0,
	}

	db, err := database.Open(dbCfg)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// A deployment must fail closed if the migration job cannot finish before
	// the orchestrator starts the new application image.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := migrate.Run(ctx, db, migrate.Options{Dir: *dir})
	if err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	if result.Adopted {
		log.Printf("existing baseline schema adopted; no development seed was replayed")
	}
	if len(result.Applied) == 0 {
		log.Printf("all migrations already applied (checksums verified); no DDL executed")
	} else {
		log.Printf("applied migrations: %v", result.Applied)
	}
	if len(result.ExtraColumns) > 0 {
		log.Printf("additive schema objects outside the required contract: columns=%v extra_indexes=%d extra_constraints=%d",
			result.ExtraColumns, result.ExtraIndexes, result.ExtraConstraints)
	}
	log.Println("migrations completed successfully")
}

func getOrDefault(flagValue, configValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return configValue
}

func getOrDefaultInt(flagValue, configValue int) int {
	if flagValue != 0 {
		return flagValue
	}
	return configValue
}
