package event

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	shortCodeConstraint   = "events_short_code_key"
	categoryFKConstraint  = "events_category_code_fkey"
)

// NewEvent is a validated event ready to be stored.
type NewEvent struct {
	HostID       string
	CategoryCode string
	Name         string
	EventDate    string // YYYY-MM-DD
	Timezone     string
	ShotLimit    *int
	RevealMode   string
	RevealAt     *time.Time
	ShortCode    string
	AccessToken  string
}

// Record is an event row as stored, without the host id or access token.
type Record struct {
	ID           string
	CategoryCode string
	Name         string
	EventDate    string
	Timezone     string
	Status       string
	ShortCode    string
	ShotLimit    *int
	RevealMode   string
	RevealAt     *time.Time
	Package      string
	CreatedAt    time.Time
}

// Repository is the storage the Service depends on.
type Repository interface {
	ListCategories(ctx context.Context) ([]Category, error)
	// GetCategory returns ErrUnknownCategory when the code does not exist.
	GetCategory(ctx context.Context, code string) (Category, error)
	// CreateEvent returns ErrShortCodeTaken when the short code is already in use.
	CreateEvent(ctx context.Context, e NewEvent) (Record, error)
	// GetEvent returns ErrNotFound unless the event exists AND belongs to hostID.
	GetEvent(ctx context.Context, hostID, eventID string) (Record, error)
}

// PostgresRepository implements Repository on the event_categories and events tables.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a Repository backed by the given database.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

const categoryColumns = `code, name, theme_key, shot_limit_default, default_shot_limit, reveal_mode_default, default_reveal_delay_hours`

type scanner interface{ Scan(dest ...any) error }

func scanCategory(row scanner) (Category, error) {
	var (
		c          Category
		shotNumber sql.NullInt32
		delayHours sql.NullInt32
	)
	if err := row.Scan(&c.Code, &c.Name, &c.ThemeKey, &c.ShotLimitDefault, &shotNumber, &c.RevealDefault, &delayHours); err != nil {
		return Category{}, err
	}
	c.DefaultShotLimit = nullableInt(shotNumber)
	c.DefaultRevealDelayHours = nullableInt(delayHours)
	return c, nil
}

func nullableInt(n sql.NullInt32) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int32)
	return &v
}

func (r *PostgresRepository) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+categoryColumns+` FROM event_categories ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}
	return out, nil
}

func (r *PostgresRepository) GetCategory(ctx context.Context, code string) (Category, error) {
	c, err := scanCategory(r.db.QueryRowContext(ctx, `SELECT `+categoryColumns+` FROM event_categories WHERE code = $1`, code))
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrUnknownCategory
	}
	if err != nil {
		return Category{}, fmt.Errorf("get category: %w", err)
	}
	return c, nil
}

const eventColumns = `event_id, category_code, name, event_date::text, timezone, status, short_code, shot_limit, reveal_mode, reveal_at, package, created_at`

func scanRecord(row scanner) (Record, error) {
	var (
		rec      Record
		shot     sql.NullInt32
		revealAt sql.NullTime
	)
	if err := row.Scan(&rec.ID, &rec.CategoryCode, &rec.Name, &rec.EventDate, &rec.Timezone, &rec.Status,
		&rec.ShortCode, &shot, &rec.RevealMode, &revealAt, &rec.Package, &rec.CreatedAt); err != nil {
		return Record{}, err
	}
	rec.ShotLimit = nullableInt(shot)
	if revealAt.Valid {
		t := revealAt.Time
		rec.RevealAt = &t
	}
	return rec, nil
}

func (r *PostgresRepository) CreateEvent(ctx context.Context, e NewEvent) (Record, error) {
	rec, err := scanRecord(r.db.QueryRowContext(ctx,
		`INSERT INTO events
		   (host_id, category_code, name, event_date, timezone, shot_limit, reveal_mode, reveal_at, short_code, access_token)
		 VALUES ($1, $2, $3, $4::date, $5, $6, $7, $8, $9, $10)
		 RETURNING `+eventColumns,
		e.HostID, e.CategoryCode, e.Name, e.EventDate, e.Timezone, e.ShotLimit, e.RevealMode, e.RevealAt, e.ShortCode, e.AccessToken))
	if err == nil {
		return rec, nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == shortCodeConstraint:
			return Record{}, ErrShortCodeTaken
		case pgErr.Code == pgForeignKeyViolation && pgErr.ConstraintName == categoryFKConstraint:
			return Record{}, ErrUnknownCategory
		}
	}
	return Record{}, fmt.Errorf("create event: %w", err)
}

// GetEvent filters by both the event id and the owning host in SQL, so another host's event is
// never even read (FSD 3.4).
func (r *PostgresRepository) GetEvent(ctx context.Context, hostID, eventID string) (Record, error) {
	rec, err := scanRecord(r.db.QueryRowContext(ctx,
		`SELECT `+eventColumns+` FROM events WHERE event_id = $1 AND host_id = $2`, eventID, hostID))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("get event: %w", err)
	}
	return rec, nil
}
