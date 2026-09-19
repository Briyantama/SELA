// Package db owns the PostgreSQL schema: embedded SQL migrations and the runner that applies them.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func newProvider(conn *sql.DB) (*goose.Provider, error) {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, files)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, nil
}

// Up applies every pending migration. It is safe to call repeatedly.
func Up(ctx context.Context, conn *sql.DB) error {
	provider, err := newProvider(conn)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// Down reverts every applied migration. Intended for tests and local resets.
func Down(ctx context.Context, conn *sql.DB) error {
	provider, err := newProvider(conn)
	if err != nil {
		return err
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("revert migrations: %w", err)
	}
	return nil
}
