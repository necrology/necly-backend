package main

import (
	"flag"
	"log"
	"os"

	"github.com/necly/services/internal/config"
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

	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	log.Println("Migrations completed successfully")
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
