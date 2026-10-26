package migrate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gorm.io/gorm"
)

type schemaExpectation struct {
	columns     []expectedColumn
	indexes     []expectedIndex
	constraints []expectedConstraint
}

type expectedColumn struct {
	table     string
	column    string
	dataType  string
	maxLength int
	nullable  bool
	def       string
	since     string
}

type expectedIndex struct {
	table   string
	columns []string
	unique  bool
	since   string
	// until marks an object that a later migration removes. It is required
	// during baseline adoption but must be absent after a full run.
	until string
}

type expectedConstraint struct {
	table      string
	kind       string
	columns    []string
	references string
	onDelete   string
	expression string
	since      string
	until      string
}

type extraSummary struct {
	Columns     []string
	Indexes     int
	Constraints int
}

type actualSchema struct {
	columns     map[string]expectedColumn
	indexes     map[string]bool
	constraints map[string]bool
}

var indexColumnsPattern = regexp.MustCompile(`(?s) USING \w+ \((.*?)\)(?: WHERE |$)`)

// verifyBaseline checks only the objects that exist after the baseline chain
// (001, 003–008). Migration 009 is applied by the runner after adoption.
func verifyBaseline(db *gorm.DB) error {
	_, err := inspectAndCompare(db, true)
	return err
}

// verifyFullSchema checks every object the chain should have produced.
func verifyFullSchema(db *gorm.DB) (extraSummary, error) {
	return inspectAndCompare(db, false)
}

func inspectAndCompare(db *gorm.DB, baselineOnly bool) (extraSummary, error) {
	actual, err := inspectSchema(db)
	if err != nil {
		return extraSummary{}, err
	}

	var differences []string
	expectedColumnKeys := make(map[string]bool)
	for _, expected := range baselineSchema.columns {
		if baselineOnly && expected.since != "baseline" {
			continue
		}
		key := expected.table + "|" + expected.column
		expectedColumnKeys[key] = true
		actualColumn, ok := actual.columns[key]
		if !ok {
			differences = append(differences, fmt.Sprintf("missing column %s.%s", expected.table, expected.column))
			continue
		}
		if actualColumn.dataType != expected.dataType {
			differences = append(differences, fmt.Sprintf("column %s.%s type is %s, expected %s", expected.table, expected.column, actualColumn.dataType, expected.dataType))
		}
		if actualColumn.maxLength != expected.maxLength {
			differences = append(differences, fmt.Sprintf("column %s.%s length is %d, expected %d", expected.table, expected.column, actualColumn.maxLength, expected.maxLength))
		}
		if actualColumn.nullable != expected.nullable {
			differences = append(differences, fmt.Sprintf("column %s.%s nullable is %t, expected %t", expected.table, expected.column, actualColumn.nullable, expected.nullable))
		}
		if strings.TrimSpace(actualColumn.def) != strings.TrimSpace(expected.def) {
			differences = append(differences, fmt.Sprintf("column %s.%s default is %q, expected %q", expected.table, expected.column, actualColumn.def, expected.def))
		}
	}

	expectedIndexKeys := make(map[string]bool)
	for _, expected := range baselineSchema.indexes {
		if baselineOnly && expected.since != "baseline" {
			continue
		}
		key := indexKey(expected.table, expected.columns, expected.unique)
		if expected.until != "" && !baselineOnly {
			if actual.indexes[key] {
				differences = append(differences, fmt.Sprintf("obsolete index still present on %s(%s) unique=%t", expected.table, strings.Join(expected.columns, ","), expected.unique))
			}
			continue
		}
		expectedIndexKeys[key] = true
		if !actual.indexes[key] {
			differences = append(differences, fmt.Sprintf("missing index on %s(%s) unique=%t", expected.table, strings.Join(expected.columns, ","), expected.unique))
		}
	}

	expectedConstraintKeys := make(map[string]bool)
	for _, expected := range baselineSchema.constraints {
		if baselineOnly && expected.since != "baseline" {
			continue
		}
		key := constraintKey(expected)
		if expected.until != "" && !baselineOnly {
			if actual.constraints[key] {
				differences = append(differences, fmt.Sprintf("obsolete %s constraint still present on %s", expected.kind, expected.table))
			}
			continue
		}
		expectedConstraintKeys[key] = true
		if !actual.constraints[key] {
			description := fmt.Sprintf("%s %s", expected.kind, expected.table)
			if len(expected.columns) > 0 {
				description += "(" + strings.Join(expected.columns, ",") + ")"
			}
			if expected.references != "" {
				description += " -> " + expected.references
			}
			if expected.expression != "" {
				description += " " + expected.expression
			}
			differences = append(differences, "missing constraint "+description)
		}
	}

	if len(differences) > 0 {
		sort.Strings(differences)
		return extraSummary{}, &SchemaDriftError{Differences: differences}
	}

	extra := extraSummary{}
	for key := range actual.columns {
		if !expectedColumnKeys[key] {
			extra.Columns = append(extra.Columns, strings.Replace(key, "|", ".", 1))
		}
	}
	sort.Strings(extra.Columns)
	for key := range actual.indexes {
		if !expectedIndexKeys[key] {
			extra.Indexes++
		}
	}
	for key := range actual.constraints {
		if !expectedConstraintKeys[key] {
			extra.Constraints++
		}
	}
	return extra, nil
}

func inspectSchema(db *gorm.DB) (*actualSchema, error) {
	schema := &actualSchema{
		columns:     make(map[string]expectedColumn),
		indexes:     make(map[string]bool),
		constraints: make(map[string]bool),
	}

	type columnRow struct {
		TableName  string
		ColumnName string
		DataType   string
		MaxLength  *int
		IsNullable string
		Default    *string
	}
	var columns []columnRow
	err := db.Raw(`SELECT table_name, column_name, data_type,
			character_maximum_length AS max_length,
			is_nullable,
			column_default AS "default"
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name <> 'schema_migrations'`).Scan(&columns).Error
	if err != nil {
		return nil, fmt.Errorf("inspect columns: %w", err)
	}
	for _, row := range columns {
		maxLength := 0
		if row.MaxLength != nil {
			maxLength = *row.MaxLength
		}
		def := ""
		if row.Default != nil {
			def = *row.Default
		}
		schema.columns[row.TableName+"|"+row.ColumnName] = expectedColumn{
			table: row.TableName, column: row.ColumnName, dataType: row.DataType,
			maxLength: maxLength, nullable: row.IsNullable == "YES", def: def,
		}
	}

	type indexRow struct {
		TableName  string
		IsUnique   bool
		Definition string
	}
	var indexes []indexRow
	err = db.Raw(`SELECT c.relname AS table_name, x.indisunique AS is_unique,
			pg_get_indexdef(x.indexrelid) AS definition
		FROM pg_index x
		JOIN pg_class c ON c.oid = x.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relname <> 'schema_migrations'`).Scan(&indexes).Error
	if err != nil {
		return nil, fmt.Errorf("inspect indexes: %w", err)
	}
	for _, row := range indexes {
		match := indexColumnsPattern.FindStringSubmatch(row.Definition)
		if match == nil {
			continue
		}
		var columns []string
		for _, part := range strings.Split(match[1], ",") {
			columns = append(columns, strings.TrimSpace(part))
		}
		schema.indexes[indexKey(row.TableName, columns, row.IsUnique)] = true
	}

	type constraintRow struct {
		TableName  string
		Kind       string
		Columns    string
		RefTable   string
		RefColumns string
		OnDelete   string
		Definition string
	}
	var constraints []constraintRow
	err = db.Raw(`SELECT c.relname AS table_name, con.contype::text AS kind,
			coalesce((
				SELECT string_agg(a.attname, ',' ORDER BY k.ord)
				FROM unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
				JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
			), '') AS columns,
			coalesce(fc.relname, '') AS ref_table,
			coalesce((
				SELECT string_agg(fa.attname, ',' ORDER BY k.ord)
				FROM unnest(con.confkey) WITH ORDINALITY AS k(attnum, ord)
				JOIN pg_attribute fa ON fa.attrelid = con.confrelid AND fa.attnum = k.attnum
			), '') AS ref_columns,
			coalesce(con.confdeltype::text, '') AS on_delete,
			coalesce(pg_get_constraintdef(con.oid), '') AS definition
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_class fc ON fc.oid = con.confrelid
		WHERE n.nspname = 'public' AND c.relname <> 'schema_migrations'
			AND con.contype IN ('p', 'u', 'f', 'c')`).Scan(&constraints).Error
	if err != nil {
		return nil, fmt.Errorf("inspect constraints: %w", err)
	}
	for _, row := range constraints {
		expected := expectedConstraint{
			table:      row.TableName,
			kind:       constraintKind(row.Kind),
			columns:    splitColumns(row.Columns),
			references: joinReference(row.RefTable, row.RefColumns),
			onDelete:   deleteRule(row.OnDelete),
			expression: row.Definition,
		}
		if expected.kind == "" {
			continue
		}
		schema.constraints[constraintKey(expected)] = true
	}

	return schema, nil
}

func constraintKind(kind string) string {
	switch kind {
	case "p":
		return "primary_key"
	case "u":
		return "unique"
	case "f":
		return "foreign_key"
	case "c":
		return "check"
	default:
		return ""
	}
}

func deleteRule(code string) string {
	switch code {
	case "a":
		return "NO ACTION"
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	default:
		return ""
	}
}

func splitColumns(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func joinReference(table, columns string) string {
	if table == "" {
		return ""
	}
	return table + "." + columns
}

func indexKey(table string, columns []string, unique bool) string {
	return fmt.Sprintf("%s|%s|%t", table, strings.Join(columns, ","), unique)
}

func constraintKey(expected expectedConstraint) string {
	if expected.kind == "check" {
		return fmt.Sprintf("%s|%s|%s", expected.table, expected.kind, expected.expression)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", expected.table, expected.kind,
		strings.Join(expected.columns, ","), expected.references, expected.onDelete)
}
