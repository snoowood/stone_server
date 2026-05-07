package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPgxAdapter wraps *pgxpool.Pool to implement DB (dormant PostgreSQL path).
func NewPgxAdapter(pool *pgxpool.Pool) DB { return &pgxDB{pool} }

type pgxDB struct{ pool *pgxpool.Pool }

func (p *pgxDB) QueryRow(ctx context.Context, query string, args ...any) Row {
	return &pgxRowAdapter{p.pool.QueryRow(ctx, query, args...)}
}

func (p *pgxDB) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRowsAdapter{rows}, nil
}

func (p *pgxDB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	tag, err := p.pool.Exec(ctx, query, args...)
	return pgxResultAdapter{tag}, err
}

func (p *pgxDB) Begin(ctx context.Context) (Tx, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &pgxTxAdapter{tx}, nil
}

func (p *pgxDB) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

// pgxRowAdapter converts pgx.ErrNoRows → sql.ErrNoRows.
type pgxRowAdapter struct{ row pgx.Row }

func (r *pgxRowAdapter) Scan(dest ...any) error {
	if err := r.row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	return nil
}

// pgxRowsAdapter adapts pgx.Rows (Close returns nothing) to store.Rows.
type pgxRowsAdapter struct{ rows pgx.Rows }

func (r *pgxRowsAdapter) Next() bool          { return r.rows.Next() }
func (r *pgxRowsAdapter) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *pgxRowsAdapter) Close() error        { r.rows.Close(); return nil }
func (r *pgxRowsAdapter) Err() error          { return r.rows.Err() }

type pgxResultAdapter struct{ tag pgconn.CommandTag }

func (r pgxResultAdapter) RowsAffected() (int64, error) { return r.tag.RowsAffected(), nil }

type pgxTxAdapter struct{ tx pgx.Tx }

func (t *pgxTxAdapter) QueryRow(ctx context.Context, query string, args ...any) Row {
	return &pgxRowAdapter{t.tx.QueryRow(ctx, query, args...)}
}

func (t *pgxTxAdapter) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	rows, err := t.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRowsAdapter{rows}, nil
}

func (t *pgxTxAdapter) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	tag, err := t.tx.Exec(ctx, query, args...)
	return pgxResultAdapter{tag}, err
}

func (t *pgxTxAdapter) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *pgxTxAdapter) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
