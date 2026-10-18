// Package migrate implements the versioned, checksum-verified SQL migration
// runner used by cmd/migrate. It replaces GORM AutoMigrate on the production
// path (W0-05): every file is applied inside a transaction, recorded in
// schema_migrations, and never silently replayed after the file changes.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const advisoryLockID int64 = 7429135601

var migrationFilePattern = regexp.MustCompile(`^(\d{3})_[a-z0-9_]+\.sql$`)

// Options configures a migration run.
type Options struct {
	// Dir is the directory that holds the SQL files. Defaults to ./migrations.
	Dir string
	// SkipSchemaCheck disables the final introspection check. It exists only
	// for the failure-path tests; production runs must verify their result.
	SkipSchemaCheck bool
}

// Result is the observable outcome of a run.
type Result struct {
	Applied          []string
	Adopted          bool
	AlreadyApplied   []string
	ExtraColumns     []string
	ExtraIndexes     int
	ExtraConstraints int
}

// Definition is one known migration file.
type Definition struct {
	Version  string
	Filename string
	Path     string
	Checksum string
	Content  []byte
}

// SchemaDriftError lists every missing or incompatible schema item.
type SchemaDriftError struct {
	Differences []string
}

func (e *SchemaDriftError) Error() string {
	return "schema drift detected: " + strings.Join(e.Differences, "; ")
}

// Run applies every pending migration in version order. It is safe to call
// repeatedly: a second run performs no DDL. When the ledger is empty but a
// pre-existing baseline schema is found, baseline versions are adopted only
// after a full structural preflight, and no historical seed is replayed.
func Run(ctx context.Context, db *gorm.DB, options Options) (*Result, error) {
	dir := options.Dir
	if dir == "" {
		dir = "migrations"
	}

	definitions, err := LoadDefinitions(dir)
	if err != nil {
		return nil, err
	}

	if err := db.WithContext(ctx).Exec("SELECT pg_advisory_lock(?)", advisoryLockID).Error; err != nil {
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// The unlock runs on a best-effort basis; the connection returning to
		// the pool also releases a session-level advisory lock.
		_ = db.Exec("SELECT pg_advisory_unlock(?)", advisoryLockID).Error
	}()

	if err := ensureLedger(db); err != nil {
		return nil, err
	}

	applied, err := loadApplied(db)
	if err != nil {
		return nil, err
	}

	known := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		known[definition.Version] = definition
	}
	if err := verifyLedger(applied, known); err != nil {
		return nil, err
	}

	result := &Result{}
	for version := range applied {
		result.AlreadyApplied = append(result.AlreadyApplied, version)
	}
	sort.Strings(result.AlreadyApplied)

	if len(applied) == 0 {
		exists, err := baselineSchemaExists(db)
		if err != nil {
			return nil, err
		}
		if exists {
			if err := verifyBaseline(db); err != nil {
				return nil, err
			}
			// Only 001 and 003–008 are adopted. Migration 009 and later are
			// applied normally below so their DDL actually runs.
			for _, definition := range definitions {
				if definition.Version > "008" {
					continue
				}
				if err := recordMigration(db, definition, "adopted"); err != nil {
					return nil, err
				}
				applied[definition.Version] = definition.Checksum
			}
			result.Adopted = true
		}
	}

	for _, definition := range definitions {
		if _, ok := applied[definition.Version]; ok {
			continue
		}
		if err := applyDefinition(ctx, db, definition); err != nil {
			return result, err
		}
		applied[definition.Version] = definition.Checksum
		result.Applied = append(result.Applied, definition.Version)
	}

	if !options.SkipSchemaCheck {
		extras, err := verifyFullSchema(db)
		if err != nil {
			return result, err
		}
		result.ExtraColumns = extras.Columns
		result.ExtraIndexes = extras.Indexes
		result.ExtraConstraints = extras.Constraints
	}

	return result, nil
}

// LoadDefinitions reads, validates and checksums every production migration.
func LoadDefinitions(dir string) ([]Definition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory %q: %w", dir, err)
	}

	seen := make(map[string]string)
	definitions := make([]Definition, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationFilePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version := match[1]
		// 002 is a development seed (W0-01). It is never part of the
		// production chain and must not be applied or adopted.
		if version == "002" {
			continue
		}
		if other, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %s: %s and %s", version, other, entry.Name())
		}
		seen[version] = entry.Name()

		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(content)
		definitions = append(definitions, Definition{
			Version:  version,
			Filename: entry.Name(),
			Path:     path,
			Checksum: hex.EncodeToString(sum[:]),
			Content:  content,
		})
	}
	if len(definitions) == 0 {
		return nil, fmt.Errorf("no migration files found in %q", dir)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Version < definitions[j].Version })
	return definitions, nil
}

func ensureLedger(db *gorm.DB) error {
	return db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		checksum char(64) NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now(),
		mode text NOT NULL CHECK (mode IN ('applied', 'adopted'))
	)`).Error
}

func loadApplied(db *gorm.DB) (map[string]string, error) {
	type row struct {
		Version  string
		Checksum string
	}
	var rows []row
	if err := db.Raw("SELECT version, checksum FROM schema_migrations").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	applied := make(map[string]string, len(rows))
	for _, item := range rows {
		applied[item.Version] = strings.TrimSpace(item.Checksum)
	}
	return applied, nil
}

func verifyLedger(applied map[string]string, known map[string]Definition) error {
	var differences []string
	for version, checksum := range applied {
		definition, ok := known[version]
		if !ok {
			differences = append(differences, fmt.Sprintf("applied version %s has no migration file", version))
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(checksum), definition.Checksum) {
			differences = append(differences,
				fmt.Sprintf("checksum mismatch for %s: ledger=%s file=%s", definition.Filename, checksum, definition.Checksum))
		}
	}
	if len(differences) > 0 {
		sort.Strings(differences)
		return fmt.Errorf("migration checksum verification failed before DDL: %s", strings.Join(differences, "; "))
	}
	return nil
}

func baselineSchemaExists(db *gorm.DB) (bool, error) {
	var exists bool
	if err := db.Raw("SELECT to_regclass('public.users') IS NOT NULL").Scan(&exists).Error; err != nil {
		return false, fmt.Errorf("inspect baseline schema: %w", err)
	}
	return exists, nil
}

func recordMigration(db *gorm.DB, definition Definition, mode string) error {
	return db.Exec(
		"INSERT INTO schema_migrations (version, checksum, mode) VALUES (?, ?, ?)",
		definition.Version, definition.Checksum, mode,
	).Error
}

func applyDefinition(ctx context.Context, db *gorm.DB, definition Definition) error {
	statements, err := SplitStatements(string(definition.Content))
	if err != nil {
		return fmt.Errorf("parse %s: %w", definition.Filename, err)
	}
	if len(statements) == 0 {
		return fmt.Errorf("migration %s contains no statements", definition.Filename)
	}

	tx := db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return fmt.Errorf("begin %s: %w", definition.Filename, tx.Error)
	}
	for index, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			_ = tx.Rollback().Error
			return fmt.Errorf("apply %s statement %d: %w", definition.Filename, index+1, err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("commit %s: %w", definition.Filename, err)
	}
	if err := recordMigration(db, definition, "applied"); err != nil {
		return fmt.Errorf("record %s: %w", definition.Filename, err)
	}
	return nil
}

// AdvisoryLockTimeout is exposed for callers that want to bound how long they
// wait for another migrator. Run itself blocks, which is the deployment-safe
// default for a one-shot migration job.
func AdvisoryLockTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}

var errStatementUnterminated = errors.New("unterminated statement")
