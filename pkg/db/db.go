package db

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	migrations "github.com/gensdeis/stone-server/migrations"
)

func NewPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}
	cfg.MaxConns = 50
	// Release idle connections after 5 minutes so memory returns to baseline
	// quickly after load ends (important for load-test acceptance criteria).
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return pool, nil
}

func RunMigrations(dbURL string) error {
	d, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, dbURL)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	m.Log = &migrateLogger{}

	before, _, _ := m.Version()
	log.Info().Uint("version", uint(before)).Msg("migrate: current db version")

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("apply migrations: %w", err)
	}

	after, _, _ := m.Version()
	log.Info().Uint("before", uint(before)).Uint("after", uint(after)).Msg("migrate: done")
	return nil
}

type migrateLogger struct{}

func (l *migrateLogger) Printf(format string, v ...interface{}) {
	log.Info().Msgf("migrate: "+format, v...)
}

func (l *migrateLogger) Verbose() bool { return true }
