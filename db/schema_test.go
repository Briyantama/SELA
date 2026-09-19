package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Briyantama/SELA/db"
	"github.com/Briyantama/SELA/internal/testdb"
)

// PostgreSQL SQLSTATE codes the schema is expected to raise.
const (
	notNullViolation    = "23502"
	foreignKeyViolation = "23503"
	uniqueViolation     = "23505"
	checkViolation      = "23514"
)

var categoryCodes = []string{"pernikahan", "wisuda", "ulang_tahun", "gathering", "reuni", "lainnya"}

var seq atomic.Int64

// migrated returns a fresh database with all migrations applied.
func migrated(t *testing.T) *sql.DB {
	t.Helper()
	conn := testdb.New(t)
	if err := db.Up(context.Background(), conn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return conn
}

func sqlState(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a PostgreSQL error, got %T: %v", err, err)
	}
	return pgErr.Code
}

func tableExists(t *testing.T, conn *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return exists
}

func newHost(t *testing.T, conn *sql.DB) string {
	t.Helper()
	n := seq.Add(1)
	var id string
	err := conn.QueryRow(
		`INSERT INTO hosts (email) VALUES ($1) RETURNING host_id`,
		fmt.Sprintf("host%d@example.test", n),
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	return id
}

// eventInput mirrors the columns the create-event flow writes.
type eventInput struct {
	hostID      string
	category    string
	name        string
	eventDate   any
	accessToken string
	shortCode   string
	shotLimit   any
	revealMode  string
	revealAt    any
}

func validEvent(hostID string) eventInput {
	n := seq.Add(1)
	return eventInput{
		hostID:      hostID,
		category:    "ulang_tahun",
		name:        "Ulang Tahun Sela",
		eventDate:   "2026-12-01",
		accessToken: fmt.Sprintf("token-%016d-abcdefghijklmnopqrstuvwx", n),
		shortCode:   fmt.Sprintf("Ab%06d", n%1000000),
		shotLimit:   nil,
		revealMode:  "instant",
		revealAt:    nil,
	}
}

func insertEvent(conn *sql.DB, in eventInput) (string, error) {
	var id string
	err := conn.QueryRow(
		`INSERT INTO events
		   (host_id, category_code, name, event_date, access_token, short_code, shot_limit, reveal_mode, reveal_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING event_id`,
		in.hostID, in.category, in.name, in.eventDate, in.accessToken, in.shortCode, in.shotLimit, in.revealMode, in.revealAt,
	).Scan(&id)
	return id, err
}

func TestUp_isIdempotent(t *testing.T) {
	// Arrange
	conn := testdb.New(t)
	ctx := context.Background()

	// Act
	first := db.Up(ctx, conn)
	second := db.Up(ctx, conn)

	// Assert
	if first != nil || second != nil {
		t.Fatalf("Up twice: first=%v second=%v, want both nil", first, second)
	}
	for _, table := range []string{"hosts", "event_categories", "events"} {
		if !tableExists(t, conn, table) {
			t.Errorf("table %s missing after Up", table)
		}
	}
}

func TestUpAndDown_reportAnErrorOnAClosedConnection(t *testing.T) {
	// Arrange
	conn := testdb.New(t)
	if err := conn.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}
	ctx := context.Background()

	// Act
	upErr := db.Up(ctx, conn)
	downErr := db.Down(ctx, conn)

	// Assert
	if upErr == nil {
		t.Error("Up on a closed connection returned nil, want an error")
	}
	if downErr == nil {
		t.Error("Down on a closed connection returned nil, want an error")
	}
}

func TestUpAndDown_rejectANilConnection(t *testing.T) {
	// Arrange
	ctx := context.Background()

	// Act
	upErr := db.Up(ctx, nil)
	downErr := db.Down(ctx, nil)

	// Assert
	if upErr == nil || downErr == nil {
		t.Fatalf("Up/Down with a nil connection: up=%v down=%v, want both to fail", upErr, downErr)
	}
}

func TestDown_removesEverythingAndUpRestoresIt(t *testing.T) {
	// Arrange
	conn := migrated(t)
	ctx := context.Background()

	// Act
	if err := db.Down(ctx, conn); err != nil {
		t.Fatalf("Down: %v", err)
	}
	goneAfterDown := !tableExists(t, conn, "events") && !tableExists(t, conn, "hosts") && !tableExists(t, conn, "event_categories")
	upAgain := db.Up(ctx, conn)

	// Assert
	if !goneAfterDown {
		t.Error("tables still exist after Down")
	}
	if upAgain != nil {
		t.Fatalf("Up after Down: %v", upAgain)
	}
	if !tableExists(t, conn, "events") {
		t.Error("events table missing after re-Up")
	}
}

func TestEventCategories_seedsTheSixFSDCategories(t *testing.T) {
	// Arrange
	conn := migrated(t)

	// Act
	rows, err := conn.Query(`SELECT code, name, theme_key FROM event_categories ORDER BY code`)
	if err != nil {
		t.Fatalf("query categories: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var code, name, theme string
		if err := rows.Scan(&code, &name, &theme); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[code] = true
		if strings.TrimSpace(name) == "" || strings.TrimSpace(theme) == "" {
			t.Errorf("category %s has an empty name or theme_key", code)
		}
	}

	// Assert
	if len(got) != len(categoryCodes) {
		t.Fatalf("got %d categories, want %d: %v", len(got), len(categoryCodes), got)
	}
	for _, code := range categoryCodes {
		if !got[code] {
			t.Errorf("category %q not seeded", code)
		}
	}
}

func TestEventCategories_onlyDocumentedDefaultsAreSet(t *testing.T) {
	// Undocumented preset values must stay TBD rather than being invented (decision D5).
	tests := []struct {
		code           string
		shotDefault    string
		revealDefault  string
		revealDelayHrs any
	}{
		{"pernikahan", "tbd", "delayed", int64(24)}, // FSD 4.5: wedding reveal delayed 1 day
		{"wisuda", "tbd", "instant", nil},           // FSD 4.5: graduation reveals instantly
		{"gathering", "tbd", "instant", nil},        // FSD 4.5: gathering reveals instantly
		{"ulang_tahun", "tbd", "tbd", nil},
		{"reuni", "tbd", "tbd", nil},
		{"lainnya", "tbd", "tbd", nil},
	}

	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			// Arrange
			conn := migrated(t)
			var shot, reveal string
			var shotNum, delay any

			// Act
			err := conn.QueryRow(
				`SELECT shot_limit_default, default_shot_limit, reveal_mode_default, default_reveal_delay_hours
				 FROM event_categories WHERE code = $1`, tc.code,
			).Scan(&shot, &shotNum, &reveal, &delay)

			// Assert
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if shot != tc.shotDefault || reveal != tc.revealDefault {
				t.Errorf("defaults = shot:%s reveal:%s, want shot:%s reveal:%s", shot, reveal, tc.shotDefault, tc.revealDefault)
			}
			if shotNum != nil {
				t.Errorf("default_shot_limit = %v, want NULL while the value is TBD", shotNum)
			}
			if delay != tc.revealDelayHrs {
				t.Errorf("default_reveal_delay_hours = %v, want %v", delay, tc.revealDelayHrs)
			}
		})
	}
}

func TestEventCategories_limitedRequiresANumber(t *testing.T) {
	// Arrange
	conn := migrated(t)

	// Act
	_, err := conn.Exec(
		`INSERT INTO event_categories (code, name, theme_key, shot_limit_default, reveal_mode_default)
		 VALUES ('custom', 'Custom', 'custom', 'limited', 'instant')`)

	// Assert
	if got := sqlState(t, err); got != checkViolation {
		t.Fatalf("SQLSTATE = %q, want %q (limited without a number)", got, checkViolation)
	}
}

func TestHosts_contactRules(t *testing.T) {
	tests := []struct {
		name  string
		email any
		phone any
		want  string // "" means success
	}{
		{"email only", "a@example.test", nil, ""},
		{"phone only", nil, "+620000000001", ""},
		{"neither contact", nil, nil, checkViolation},
		{"empty email", "", nil, checkViolation},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			conn := migrated(t)

			// Act
			_, err := conn.Exec(`INSERT INTO hosts (email, phone) VALUES ($1, $2)`, tc.email, tc.phone)

			// Assert
			if got := sqlState(t, err); got != tc.want {
				t.Fatalf("SQLSTATE = %q, want %q (err=%v)", got, tc.want, err)
			}
		})
	}
}

func TestHosts_emailAndPhoneAreUnique(t *testing.T) {
	// Arrange
	conn := migrated(t)
	if _, err := conn.Exec(`INSERT INTO hosts (email, phone) VALUES ('Dup@Example.test', '+620000000002')`); err != nil {
		t.Fatalf("seed host: %v", err)
	}

	// Act
	_, sameEmailDifferentCase := conn.Exec(`INSERT INTO hosts (email) VALUES ('dup@example.TEST')`)
	_, samePhone := conn.Exec(`INSERT INTO hosts (phone) VALUES ('+620000000002')`)
	_, otherHostsWithoutEmail1 := conn.Exec(`INSERT INTO hosts (phone) VALUES ('+620000000003')`)
	_, otherHostsWithoutEmail2 := conn.Exec(`INSERT INTO hosts (phone) VALUES ('+620000000004')`)

	// Assert
	if got := sqlState(t, sameEmailDifferentCase); got != uniqueViolation {
		t.Errorf("duplicate email SQLSTATE = %q, want %q", got, uniqueViolation)
	}
	if got := sqlState(t, samePhone); got != uniqueViolation {
		t.Errorf("duplicate phone SQLSTATE = %q, want %q", got, uniqueViolation)
	}
	if otherHostsWithoutEmail1 != nil || otherHostsWithoutEmail2 != nil {
		t.Errorf("several hosts without an email must be allowed: %v / %v", otherHostsWithoutEmail1, otherHostsWithoutEmail2)
	}
}

func TestEvents_minimalInsertGetsSafeDefaults(t *testing.T) {
	// Arrange
	conn := migrated(t)
	host := newHost(t, conn)

	// Act
	id, err := insertEvent(conn, validEvent(host))
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	var status, pkg, tz string
	var shotLimit sql.NullInt64
	var createdAt time.Time
	err = conn.QueryRow(
		`SELECT status, package, timezone, shot_limit, created_at FROM events WHERE event_id = $1`, id,
	).Scan(&status, &pkg, &tz, &shotLimit, &createdAt)

	// Assert
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	if status != "active" || pkg != "gratis" || tz != "Asia/Jakarta" {
		t.Errorf("defaults = status:%s package:%s timezone:%s, want active/gratis/Asia/Jakarta", status, pkg, tz)
	}
	if shotLimit.Valid {
		t.Errorf("shot_limit = %d, want NULL (unlimited)", shotLimit.Int64)
	}
	if createdAt.IsZero() {
		t.Error("created_at not set")
	}
}

func TestEvents_idsAreRandomNotSequential(t *testing.T) {
	// Arrange
	conn := migrated(t)
	host := newHost(t, conn)

	// Act
	a, errA := insertEvent(conn, validEvent(host))
	b, errB := insertEvent(conn, validEvent(host))

	// Assert
	if errA != nil || errB != nil {
		t.Fatalf("insert: %v / %v", errA, errB)
	}
	if a == b || len(a) != 36 || len(b) != 36 || strings.Count(a, "-") != 4 {
		t.Fatalf("event ids should be distinct UUIDs, got %q and %q", a, b)
	}
}

func TestEvents_categoryIsAPresetNotAGate(t *testing.T) {
	// Every category must accept every combination of shot limit and reveal mode.
	for _, category := range categoryCodes {
		for _, limit := range []any{nil, 5} {
			for _, mode := range []string{"instant", "delayed"} {
				name := fmt.Sprintf("%s/limit=%v/%s", category, limit, mode)
				t.Run(name, func(t *testing.T) {
					// Arrange
					conn := migrated(t)
					in := validEvent(newHost(t, conn))
					in.category, in.shotLimit, in.revealMode = category, limit, mode
					if mode == "delayed" {
						in.revealAt = time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC)
					}

					// Act
					_, err := insertEvent(conn, in)

					// Assert
					if err != nil {
						t.Fatalf("insert failed: %v", err)
					}
				})
			}
		}
	}
}

func TestEvents_rejectsInvalidRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *eventInput)
		want   string
	}{
		{"empty name", func(in *eventInput) { in.name = "" }, checkViolation},
		{"missing date", func(in *eventInput) { in.eventDate = nil }, notNullViolation},
		{"unknown category", func(in *eventInput) { in.category = "konser" }, foreignKeyViolation},
		{"unknown host", func(in *eventInput) { in.hostID = "00000000-0000-0000-0000-000000000000" }, foreignKeyViolation},
		{"short code too short", func(in *eventInput) { in.shortCode = "abc" }, checkViolation},
		{"short code too long", func(in *eventInput) { in.shortCode = "abcdefghi" }, checkViolation},
		{"short code not base62", func(in *eventInput) { in.shortCode = "abcdef!?" }, checkViolation},
		{"shot limit zero", func(in *eventInput) { in.shotLimit = 0 }, checkViolation},
		{"shot limit negative", func(in *eventInput) { in.shotLimit = -3 }, checkViolation},
		{"unknown reveal mode", func(in *eventInput) { in.revealMode = "later" }, checkViolation},
		{"delayed reveal without a time", func(in *eventInput) { in.revealMode, in.revealAt = "delayed", nil }, checkViolation},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			conn := migrated(t)
			in := validEvent(newHost(t, conn))
			tc.mutate(&in)

			// Act
			_, err := insertEvent(conn, in)

			// Assert
			if got := sqlState(t, err); got != tc.want {
				t.Fatalf("SQLSTATE = %q, want %q (err=%v)", got, tc.want, err)
			}
		})
	}
}

func TestEvents_shortCodeAndAccessTokenAreUnique(t *testing.T) {
	// Arrange
	conn := migrated(t)
	host := newHost(t, conn)
	first := validEvent(host)
	if _, err := insertEvent(conn, first); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	sameCode := validEvent(host)
	sameCode.shortCode = first.shortCode
	sameToken := validEvent(host)
	sameToken.accessToken = first.accessToken

	// Act
	_, codeErr := insertEvent(conn, sameCode)
	_, tokenErr := insertEvent(conn, sameToken)

	// Assert
	if got := sqlState(t, codeErr); got != uniqueViolation {
		t.Errorf("duplicate short_code SQLSTATE = %q, want %q", got, uniqueViolation)
	}
	if got := sqlState(t, tokenErr); got != uniqueViolation {
		t.Errorf("duplicate access_token SQLSTATE = %q, want %q", got, uniqueViolation)
	}
}

func TestEvents_invalidPackageOrStatusIsRejected(t *testing.T) {
	// Arrange
	conn := migrated(t)
	id, err := insertEvent(conn, validEvent(newHost(t, conn)))
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}

	// Act
	_, badPackage := conn.Exec(`UPDATE events SET package = 'diamond' WHERE event_id = $1`, id)
	_, badStatus := conn.Exec(`UPDATE events SET status = 'archived' WHERE event_id = $1`, id)

	// Assert
	if got := sqlState(t, badPackage); got != checkViolation {
		t.Errorf("bad package SQLSTATE = %q, want %q", got, checkViolation)
	}
	if got := sqlState(t, badStatus); got != checkViolation {
		t.Errorf("bad status SQLSTATE = %q, want %q", got, checkViolation)
	}
}

func TestEvents_ownershipFilterScopesToTheHost(t *testing.T) {
	// Arrange
	conn := migrated(t)
	owner := newHost(t, conn)
	stranger := newHost(t, conn)
	id, err := insertEvent(conn, validEvent(owner))
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	const scoped = `SELECT count(*) FROM events WHERE event_id = $1 AND host_id = $2`

	// Act
	var ownerRows, strangerRows int
	errOwner := conn.QueryRow(scoped, id, owner).Scan(&ownerRows)
	errStranger := conn.QueryRow(scoped, id, stranger).Scan(&strangerRows)

	// Assert
	if errOwner != nil || errStranger != nil {
		t.Fatalf("query: %v / %v", errOwner, errStranger)
	}
	if ownerRows != 1 || strangerRows != 0 {
		t.Fatalf("owner sees %d rows and stranger sees %d, want 1 and 0", ownerRows, strangerRows)
	}
}

func TestEvents_hostQueriesAreIndexed(t *testing.T) {
	// Arrange
	conn := migrated(t)

	// Act
	var indexDef string
	err := conn.QueryRow(
		`SELECT indexdef FROM pg_indexes
		 WHERE schemaname = 'public' AND tablename = 'events' AND indexdef ILIKE '%(host_id, created_at DESC)%'`,
	).Scan(&indexDef)

	// Assert
	if err != nil {
		t.Fatalf("expected an index on events (host_id, created_at DESC): %v", err)
	}
}

func TestHosts_cannotBeDeletedWhileTheyOwnEvents(t *testing.T) {
	// Arrange
	conn := migrated(t)
	host := newHost(t, conn)
	if _, err := insertEvent(conn, validEvent(host)); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	// Act
	_, err := conn.Exec(`DELETE FROM hosts WHERE host_id = $1`, host)

	// Assert
	if got := sqlState(t, err); got != foreignKeyViolation {
		t.Fatalf("SQLSTATE = %q, want %q", got, foreignKeyViolation)
	}
}
