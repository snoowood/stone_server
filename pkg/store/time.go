package store

import (
	"database/sql"
	"fmt"
	"time"
)

var timeFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
}

func parseTimeStr(s string) (time.Time, error) {
	for _, f := range timeFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("store: cannot parse time %q", s)
}

// ScanTime returns an sql.Scanner that stores a TEXT timestamp into *t.
// Use in Scan calls: rows.Scan(store.ScanTime(&myTimeField))
func ScanTime(t *time.Time) sql.Scanner { return &timeScanner{p: t} }

type timeScanner struct{ p *time.Time }

func (ts *timeScanner) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*ts.p = v.UTC()
	case string:
		parsed, err := parseTimeStr(v)
		if err != nil {
			return err
		}
		*ts.p = parsed
	case nil:
		*ts.p = time.Time{}
	default:
		return fmt.Errorf("store.ScanTime: unsupported type %T", src)
	}
	return nil
}

// ScanNullTime returns an sql.Scanner that stores a nullable TEXT timestamp into **t.
// Sets *t = nil when the column is NULL.
func ScanNullTime(t **time.Time) sql.Scanner { return &nullTimeScanner{p: t} }

type nullTimeScanner struct{ p **time.Time }

func (ts *nullTimeScanner) Scan(src any) error {
	if src == nil {
		*ts.p = nil
		return nil
	}
	var inner time.Time
	if err := (&timeScanner{p: &inner}).Scan(src); err != nil {
		return err
	}
	*ts.p = &inner
	return nil
}
