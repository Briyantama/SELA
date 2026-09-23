package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EventInfo is what the guest flow needs to know about an event.
type EventInfo struct {
	ID             string
	ShotLimit      *int // nil = unlimited
	InvolvesMinors bool
}

// PendingMedia is a row created when an upload URL is issued.
type PendingMedia struct {
	ID           string
	EventID      string
	SessionID    string
	Kind         Kind
	ContentType  string
	ObjectKey    string
	DeclaredSize int64
	UploadedAt   time.Time
}

// Expired is a pending upload the sweeper gave up on.
type Expired struct {
	MediaID   string
	EventID   string
	SessionID *string
	ObjectKey string
	ShotLimit *int
}

// Repository is the media persistence. Every read filters by the caller's scope in SQL (FSD 3.4):
// a guest by event_id + session_id, the host by event_id + ownership.
type Repository interface {
	// GuestEvent returns an event guests may join now: active and not past its expiry.
	GuestEvent(ctx context.Context, eventID string, now time.Time) (EventInfo, error)
	CreateSession(ctx context.Context, eventID string, nickname *string) (string, error)
	// CountShots counts a session's shots that are spent or in flight (everything but failed uploads).
	CountShots(ctx context.Context, eventID, sessionID string) (int, error)
	CreatePending(ctx context.Context, p PendingMedia) error
	// PendingUpload returns the caller's own pending item and the event's shot limit.
	PendingUpload(ctx context.Context, eventID, sessionID, mediaID string) (Media, *int, error)
	MarkReady(ctx context.Context, mediaID, objectKey string, size int64, now time.Time) (bool, error)
	MarkFailed(ctx context.Context, mediaID string) (bool, error)
	ExpirePending(ctx context.Context, before time.Time) ([]Expired, error)
	ListSessionMedia(ctx context.Context, eventID, sessionID string) ([]Media, error)
	ListHallOfFame(ctx context.Context, eventID string, now time.Time) ([]Media, error)
	ListHostMedia(ctx context.Context, hostID, eventID string) ([]Media, error)
}

// PostgresRepository implements Repository on migration 0006's tables.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a repository on db.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

const mediaColumns = `m.media_id, m.event_id, m.session_id, m.kind, m.content_type, m.object_key, m.size_bytes,
	m.processing_state, m.status, m.hall_of_fame, m.hall_of_fame_rank, m.reaction_count,
	m.uploaded_at, m.ready_at, m.deleted_at`

func (r *PostgresRepository) GuestEvent(ctx context.Context, eventID string, now time.Time) (EventInfo, error) {
	var ev EventInfo
	var shotLimit sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		`SELECT event_id, shot_limit, involves_minors FROM events
		  WHERE event_id = $1 AND status = 'active' AND (expires_at IS NULL OR expires_at > $2)`,
		eventID, now,
	).Scan(&ev.ID, &shotLimit, &ev.InvolvesMinors)
	if errors.Is(err, sql.ErrNoRows) {
		return EventInfo{}, ErrNotFound
	}
	if err != nil {
		return EventInfo{}, fmt.Errorf("read guest event: %w", err)
	}
	ev.ShotLimit = intPtr(shotLimit)
	return ev, nil
}

func (r *PostgresRepository) CreateSession(ctx context.Context, eventID string, nickname *string) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO guest_sessions (event_id, nickname) VALUES ($1, $2) RETURNING session_id`,
		eventID, nickname,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create guest session: %w", err)
	}
	return id, nil
}

func (r *PostgresRepository) CountShots(ctx context.Context, eventID, sessionID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM media WHERE event_id = $1 AND session_id = $2 AND processing_state <> 'failed'`,
		eventID, sessionID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count shots: %w", err)
	}
	return n, nil
}

func (r *PostgresRepository) CreatePending(ctx context.Context, p PendingMedia) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO media (media_id, event_id, session_id, kind, content_type, object_key, size_bytes, uploaded_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		p.ID, p.EventID, p.SessionID, string(p.Kind), p.ContentType, p.ObjectKey, p.DeclaredSize, p.UploadedAt,
	)
	if err != nil {
		return fmt.Errorf("create pending media: %w", err)
	}
	return nil
}

func (r *PostgresRepository) PendingUpload(ctx context.Context, eventID, sessionID, mediaID string) (Media, *int, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+mediaColumns+`, e.shot_limit
		   FROM media m JOIN events e ON e.event_id = m.event_id
		  WHERE m.media_id = $3 AND m.event_id = $1 AND m.session_id = $2
		    AND m.processing_state = 'pending' AND m.deleted_at IS NULL`,
		eventID, sessionID, mediaID,
	)
	var shotLimit sql.NullInt64
	m, err := scanMedia(row, &shotLimit)
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, nil, ErrNotFound
	}
	if err != nil {
		return Media{}, nil, fmt.Errorf("read pending upload: %w", err)
	}
	return m, intPtr(shotLimit), nil
}

func (r *PostgresRepository) MarkReady(ctx context.Context, mediaID, objectKey string, size int64, now time.Time) (bool, error) {
	return r.transition(ctx,
		`UPDATE media SET processing_state = 'ready', object_key = $2, size_bytes = $3, ready_at = $4
		  WHERE media_id = $1 AND processing_state = 'pending'`,
		mediaID, objectKey, size, now)
}

func (r *PostgresRepository) MarkFailed(ctx context.Context, mediaID string) (bool, error) {
	return r.transition(ctx,
		`UPDATE media SET processing_state = 'failed' WHERE media_id = $1 AND processing_state = 'pending'`,
		mediaID)
}

// transition runs a guarded state change and reports whether this caller made it.
func (r *PostgresRepository) transition(ctx context.Context, query string, args ...any) (bool, error) {
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("update media state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update media state: %w", err)
	}
	return n == 1, nil
}

func (r *PostgresRepository) ExpirePending(ctx context.Context, before time.Time) ([]Expired, error) {
	rows, err := r.db.QueryContext(ctx,
		`UPDATE media m SET processing_state = 'failed'
		   FROM events e
		  WHERE e.event_id = m.event_id AND m.processing_state = 'pending' AND m.uploaded_at < $1
		 RETURNING m.media_id, m.event_id, m.session_id, m.object_key, e.shot_limit`,
		before,
	)
	if err != nil {
		return nil, fmt.Errorf("expire pending uploads: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var expired []Expired
	for rows.Next() {
		var x Expired
		var sessionID sql.NullString
		var shotLimit sql.NullInt64
		if err := rows.Scan(&x.MediaID, &x.EventID, &sessionID, &x.ObjectKey, &shotLimit); err != nil {
			return nil, fmt.Errorf("scan expired upload: %w", err)
		}
		if sessionID.Valid {
			x.SessionID = &sessionID.String
		}
		x.ShotLimit = intPtr(shotLimit)
		expired = append(expired, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("expire pending uploads: %w", err)
	}
	return expired, nil
}

// ListSessionMedia is the guest's own gallery (FR-05.5): event_id AND session_id, never wider.
func (r *PostgresRepository) ListSessionMedia(ctx context.Context, eventID, sessionID string) ([]Media, error) {
	return r.list(ctx,
		`SELECT `+mediaColumns+` FROM media m
		  WHERE m.event_id = $1 AND m.session_id = $2 AND m.deleted_at IS NULL
		  ORDER BY m.uploaded_at DESC, m.media_id DESC`,
		eventID, sessionID)
}

// ListHallOfFame is the only view of other guests' media (FSD 3.2): the host-curated, visible, processed
// subset, and only once the event's reveal has passed, all decided in the query.
func (r *PostgresRepository) ListHallOfFame(ctx context.Context, eventID string, now time.Time) ([]Media, error) {
	return r.list(ctx,
		`SELECT `+mediaColumns+` FROM media m JOIN events e ON e.event_id = m.event_id
		  WHERE m.event_id = $1 AND m.hall_of_fame AND m.status = 'active' AND m.processing_state = 'ready'
		    AND m.deleted_at IS NULL AND (e.reveal_mode = 'instant' OR e.reveal_at <= $2)
		  ORDER BY m.hall_of_fame_rank`,
		eventID, now)
}

// ListHostMedia is the owner's gallery (FR-05.6): every non-deleted item, whatever the reveal state.
func (r *PostgresRepository) ListHostMedia(ctx context.Context, hostID, eventID string) ([]Media, error) {
	var owned bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM events WHERE event_id = $1 AND host_id = $2)`, eventID, hostID,
	).Scan(&owned)
	if err != nil {
		return nil, fmt.Errorf("check event owner: %w", err)
	}
	if !owned {
		return nil, ErrNotFound
	}
	return r.list(ctx,
		`SELECT `+mediaColumns+` FROM media m JOIN events e ON e.event_id = m.event_id
		  WHERE m.event_id = $1 AND e.host_id = $2 AND m.deleted_at IS NULL
		  ORDER BY m.uploaded_at DESC, m.media_id DESC`,
		eventID, hostID)
}

func (r *PostgresRepository) list(ctx context.Context, query string, args ...any) ([]Media, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list media: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := []Media{}
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, fmt.Errorf("scan media: %w", err)
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list media: %w", err)
	}
	return items, nil
}

type scanner interface{ Scan(dest ...any) error }

// scanMedia reads mediaColumns, then any extra destinations the query appends.
func scanMedia(row scanner, extra ...any) (Media, error) {
	var m Media
	var sessionID sql.NullString
	var kind, processing, status string
	var size, rank sql.NullInt64
	var readyAt, deletedAt sql.NullTime
	dest := append([]any{
		&m.ID, &m.EventID, &sessionID, &kind, &m.ContentType, &m.ObjectKey, &size,
		&processing, &status, &m.HallOfFame, &rank, &m.ReactionCount,
		&m.UploadedAt, &readyAt, &deletedAt,
	}, extra...)
	if err := row.Scan(dest...); err != nil {
		return Media{}, err
	}
	if sessionID.Valid {
		m.SessionID = &sessionID.String
	}
	m.Kind, m.Processing, m.Status = Kind(kind), ProcessingState(processing), Status(status)
	if size.Valid {
		m.SizeBytes = &size.Int64
	}
	m.HallOfFameRank = intPtr(rank)
	if readyAt.Valid {
		m.ReadyAt = &readyAt.Time
	}
	if deletedAt.Valid {
		m.DeletedAt = &deletedAt.Time
	}
	return m, nil
}

func intPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}
