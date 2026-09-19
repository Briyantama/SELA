package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// HostRepository finds or creates the host that owns an email address.
type HostRepository interface {
	// FindOrCreateByEmail returns the host id for the address and whether this call created the host.
	FindOrCreateByEmail(ctx context.Context, email string) (hostID string, created bool, err error)
}

// PostgresHosts implements HostRepository on the hosts table.
type PostgresHosts struct {
	db *sql.DB
}

// NewPostgresHosts returns a HostRepository backed by the given database.
func NewPostgresHosts(db *sql.DB) *PostgresHosts {
	return &PostgresHosts{db: db}
}

// FindOrCreateByEmail is safe under concurrent first sign-ins: the unique email index
// guarantees a single row, and only the call that inserted it reports created=true.
func (h *PostgresHosts) FindOrCreateByEmail(ctx context.Context, email string) (string, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	var id string
	err := h.db.QueryRowContext(ctx,
		`INSERT INTO hosts (email) VALUES ($1)
		 ON CONFLICT (lower(email)) WHERE email IS NOT NULL DO NOTHING
		 RETURNING host_id`, email).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("create host: %w", err)
	}

	if err := h.db.QueryRowContext(ctx,
		`SELECT host_id FROM hosts WHERE lower(email) = $1`, email).Scan(&id); err != nil {
		return "", false, fmt.Errorf("find host: %w", err)
	}
	return id, false, nil
}
