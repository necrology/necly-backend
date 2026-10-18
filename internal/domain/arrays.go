package domain

import (
	"database/sql/driver"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

// StringArray and IntArray map PostgreSQL text[]/integer[] columns without
// pulling GORM into the domain package. They implement the standard
// sql.Scanner/driver.Valuer contract, so a nil slice is stored as NULL (the
// column defaults handle new rows) and JSON stays a plain array.
type StringArray []string

func (a *StringArray) Scan(src any) error {
	if src == nil {
		*a = nil
		return nil
	}
	raw, err := arrayBytes(src)
	if err != nil {
		return fmt.Errorf("scan text array: %w", err)
	}
	var decoded pgtype.Array[string]
	if err := pgtype.NewMap().Scan(pgtype.TextArrayOID, pgtype.TextFormatCode, raw, &decoded); err != nil {
		return err
	}
	*a = decoded.Elements
	return nil
}

func (a StringArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	encoded, err := pgtype.NewMap().Encode(
		pgtype.TextArrayOID,
		pgtype.TextFormatCode,
		&pgtype.Array[string]{
			Elements: a,
			Dims:     []pgtype.ArrayDimension{{Length: int32(len(a)), LowerBound: 1}},
			Valid:    true,
		},
		nil,
	)
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}

type IntArray []int

func (a *IntArray) Scan(src any) error {
	if src == nil {
		*a = nil
		return nil
	}
	raw, err := arrayBytes(src)
	if err != nil {
		return fmt.Errorf("scan integer array: %w", err)
	}
	var decoded pgtype.Array[int]
	if err := pgtype.NewMap().Scan(pgtype.Int4ArrayOID, pgtype.TextFormatCode, raw, &decoded); err != nil {
		return err
	}
	*a = decoded.Elements
	return nil
}

func (a IntArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	encoded, err := pgtype.NewMap().Encode(
		pgtype.Int4ArrayOID,
		pgtype.TextFormatCode,
		&pgtype.Array[int]{
			Elements: a,
			Dims:     []pgtype.ArrayDimension{{Length: int32(len(a)), LowerBound: 1}},
			Valid:    true,
		},
		nil,
	)
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}

// arrayBytes accepts the two shapes database drivers use for an array value:
// []byte (lib/pq) and string (pgx stdlib).
func arrayBytes(src any) ([]byte, error) {
	switch value := src.(type) {
	case []byte:
		return value, nil
	case string:
		return []byte(value), nil
	default:
		return nil, fmt.Errorf("unsupported source %T", src)
	}
}
