package store

import (
	"context"
	"database/sql"
)

// ErrNoRows is returned when a query finds no rows.
var ErrNoRows = sql.ErrNoRows

// Row is the minimal scan interface satisfied by *sql.Row and pgx.Row.
type Row interface {
	Scan(dest ...any) error
}

// Rows is the minimal cursor interface satisfied by *sql.Rows and pgx.Rows.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// Result is the minimal command result interface satisfied by sql.Result.
type Result interface {
	RowsAffected() (int64, error)
}

// Tx is a database transaction.
type Tx interface {
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// DB is the minimal database interface used throughout the server.
type DB interface {
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Begin(ctx context.Context) (Tx, error)
	Ping(ctx context.Context) error
}
