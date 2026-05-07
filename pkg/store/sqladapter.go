package store

import (
	"context"
	"database/sql"
)

type sqlDB struct{ db *sql.DB }

// NewSQLAdapter wraps *sql.DB to implement DB (active SQLite path).
func NewSQLAdapter(db *sql.DB) DB { return &sqlDB{db} }

func (s *sqlDB) QueryRow(ctx context.Context, query string, args ...any) Row {
	return s.db.QueryRowContext(ctx, query, args...)
}

func (s *sqlDB) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	return s.db.QueryContext(ctx, query, args...)
}

func (s *sqlDB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	return s.db.ExecContext(ctx, query, args...)
}

func (s *sqlDB) Begin(ctx context.Context) (Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &sqlTx{tx}, nil
}

func (s *sqlDB) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

type sqlTx struct{ tx *sql.Tx }

func (t *sqlTx) QueryRow(ctx context.Context, query string, args ...any) Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

func (t *sqlTx) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}

func (t *sqlTx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}

func (t *sqlTx) Commit(_ context.Context) error   { return t.tx.Commit() }
func (t *sqlTx) Rollback(_ context.Context) error { return t.tx.Rollback() }
