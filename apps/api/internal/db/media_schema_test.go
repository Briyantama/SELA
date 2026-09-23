package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/Briyantama/SELA/internal/db"
)

func newEvent(t *testing.T, conn *sql.DB) string {
	t.Helper()
	id, err := insertEvent(conn, validEvent(newHost(t, conn)))
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return id
}

func newGuestSession(t *testing.T, conn *sql.DB, eventID string) string {
	t.Helper()
	var id string
	err := conn.QueryRow(`INSERT INTO guest_sessions (event_id) VALUES ($1) RETURNING session_id`, eventID).Scan(&id)
	if err != nil {
		t.Fatalf("insert guest session: %v", err)
	}
	return id
}

// mediaInput mirrors the columns the upload flow writes.
type mediaInput struct {
	eventID     string
	sessionID   any
	kind        string
	contentType string
	objectKey   string
}

func validMedia(eventID string, sessionID any) mediaInput {
	n := seq.Add(1)
	return mediaInput{
		eventID:     eventID,
		sessionID:   sessionID,
		kind:        "photo",
		contentType: "image/jpeg",
		objectKey:   fmt.Sprintf("events/%s/media/%08d.jpg", eventID, n),
	}
}

func insertMedia(conn *sql.DB, in mediaInput) (string, error) {
	var id string
	err := conn.QueryRow(
		`INSERT INTO media (event_id, session_id, kind, content_type, object_key)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING media_id`,
		in.eventID, in.sessionID, in.kind, in.contentType, in.objectKey,
	).Scan(&id)
	return id, err
}

func markReady(t *testing.T, conn *sql.DB, mediaID string) {
	t.Helper()
	_, err := conn.Exec(
		`UPDATE media SET processing_state = 'ready', size_bytes = 1024, ready_at = now() WHERE media_id = $1`, mediaID)
	if err != nil {
		t.Fatalf("mark ready: %v", err)
	}
}

func TestMigration0004_createsTheTablesAndDownRemovesThem(t *testing.T) {
	// Arrange
	conn := migrated(t)
	ctx := context.Background()
	createdByUp := tableExists(t, conn, "guest_sessions") && tableExists(t, conn, "media")

	// Act
	if err := db.Down(ctx, conn); err != nil {
		t.Fatalf("Down: %v", err)
	}

	// Assert
	if !createdByUp {
		t.Fatal("guest_sessions or media missing after Up")
	}
	if tableExists(t, conn, "guest_sessions") || tableExists(t, conn, "media") {
		t.Fatal("guest_sessions or media still exist after Down")
	}
}

func TestEvents_involvesMinorsDefaultsToFalse(t *testing.T) {
	// Arrange
	conn := migrated(t)
	id := newEvent(t, conn)

	// Act
	var involvesMinors bool
	err := conn.QueryRow(`SELECT involves_minors FROM events WHERE event_id = $1`, id).Scan(&involvesMinors)

	// Assert
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	if involvesMinors {
		t.Fatal("involves_minors = true, want false by default")
	}
}

func TestGuestSessions_nicknameRules(t *testing.T) {
	tests := []struct {
		name     string
		nickname any
		want     string
	}{
		{"no nickname", nil, ""},
		{"short nickname", "Tante Rina", ""},
		{"blank nickname", "   ", checkViolation},
		{"41 characters", "abcdefghijabcdefghijabcdefghijabcdefghijk", checkViolation},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			conn := migrated(t)
			eventID := newEvent(t, conn)

			// Act
			_, err := conn.Exec(`INSERT INTO guest_sessions (event_id, nickname) VALUES ($1, $2)`, eventID, tc.nickname)

			// Assert
			if got := sqlState(t, err); got != tc.want {
				t.Fatalf("SQLSTATE = %q, want %q (err=%v)", got, tc.want, err)
			}
		})
	}
}

func TestMedia_minimalInsertStartsPendingAndActive(t *testing.T) {
	// Arrange
	conn := migrated(t)
	eventID := newEvent(t, conn)
	sessionID := newGuestSession(t, conn, eventID)

	// Act
	id, err := insertMedia(conn, validMedia(eventID, sessionID))
	if err != nil {
		t.Fatalf("insert media: %v", err)
	}
	var processing, status string
	var hallOfFame bool
	var reactions int
	err = conn.QueryRow(
		`SELECT processing_state, status, hall_of_fame, reaction_count FROM media WHERE media_id = $1`, id,
	).Scan(&processing, &status, &hallOfFame, &reactions)

	// Assert
	if err != nil {
		t.Fatalf("read media: %v", err)
	}
	if processing != "pending" || status != "active" || hallOfFame || reactions != 0 {
		t.Fatalf("defaults = %s/%s/hof:%v/reactions:%d, want pending/active/false/0", processing, status, hallOfFame, reactions)
	}
}

func TestMedia_hostUploadHasNoSession(t *testing.T) {
	// Arrange
	conn := migrated(t)
	eventID := newEvent(t, conn)

	// Act
	_, err := insertMedia(conn, validMedia(eventID, nil))

	// Assert
	if err != nil {
		t.Fatalf("host upload without a session: %v", err)
	}
}

func TestMedia_sessionMustBelongToTheSameEvent(t *testing.T) {
	// A guest session of event A can never own media of event B: the access boundary lives in the schema.
	// Arrange
	conn := migrated(t)
	eventA := newEvent(t, conn)
	eventB := newEvent(t, conn)
	sessionOfA := newGuestSession(t, conn, eventA)

	// Act
	_, err := insertMedia(conn, validMedia(eventB, sessionOfA))

	// Assert
	if got := sqlState(t, err); got != foreignKeyViolation {
		t.Fatalf("SQLSTATE = %q, want %q", got, foreignKeyViolation)
	}
}

func TestMedia_rejectsInvalidRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *mediaInput)
		want   string
	}{
		{"unknown kind", func(in *mediaInput) { in.kind = "audio" }, checkViolation},
		{"photo with a video type", func(in *mediaInput) { in.contentType = "video/mp4" }, checkViolation},
		{"unsupported image type", func(in *mediaInput) { in.contentType = "image/gif" }, checkViolation},
		{"video with an image type", func(in *mediaInput) { in.kind = "video" }, checkViolation},
		{"empty object key", func(in *mediaInput) { in.objectKey = " " }, checkViolation},
		{"unknown event", func(in *mediaInput) { in.eventID = "00000000-0000-0000-0000-000000000000" }, foreignKeyViolation},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			conn := migrated(t)
			in := validMedia(newEvent(t, conn), nil)
			tc.mutate(&in)

			// Act
			_, err := insertMedia(conn, in)

			// Assert
			if got := sqlState(t, err); got != tc.want {
				t.Fatalf("SQLSTATE = %q, want %q (err=%v)", got, tc.want, err)
			}
		})
	}
}

func TestMedia_acceptsEveryDocumentedFormat(t *testing.T) {
	formats := []struct{ kind, contentType string }{
		{"photo", "image/jpeg"}, {"photo", "image/png"}, {"photo", "image/heic"}, {"photo", "image/webp"},
		{"video", "video/mp4"}, {"video", "video/quicktime"},
	}

	for _, f := range formats {
		t.Run(f.contentType, func(t *testing.T) {
			// Arrange
			conn := migrated(t)
			in := validMedia(newEvent(t, conn), nil)
			in.kind, in.contentType = f.kind, f.contentType

			// Act
			_, err := insertMedia(conn, in)

			// Assert
			if err != nil {
				t.Fatalf("insert %s: %v", f.contentType, err)
			}
		})
	}
}

func TestMedia_objectKeyIsUnique(t *testing.T) {
	// Arrange
	conn := migrated(t)
	eventID := newEvent(t, conn)
	first := validMedia(eventID, nil)
	if _, err := insertMedia(conn, first); err != nil {
		t.Fatalf("seed media: %v", err)
	}
	second := validMedia(eventID, nil)
	second.objectKey = first.objectKey

	// Act
	_, err := insertMedia(conn, second)

	// Assert
	if got := sqlState(t, err); got != uniqueViolation {
		t.Fatalf("SQLSTATE = %q, want %q", got, uniqueViolation)
	}
}

func TestMedia_lifecycleRules(t *testing.T) {
	tests := []struct {
		name   string
		update string
		want   string
	}{
		{"ready needs a size and time", `UPDATE media SET processing_state = 'ready' WHERE media_id = $1`, checkViolation},
		{"ready with size and time", `UPDATE media SET processing_state = 'ready', size_bytes = 10, ready_at = now() WHERE media_id = $1`, ""},
		{"unknown processing state", `UPDATE media SET processing_state = 'done' WHERE media_id = $1`, checkViolation},
		{"unknown status", `UPDATE media SET status = 'archived' WHERE media_id = $1`, checkViolation},
		{"deleted needs a time", `UPDATE media SET status = 'deleted' WHERE media_id = $1`, checkViolation},
		{"deleted with a time", `UPDATE media SET status = 'deleted', deleted_at = now() WHERE media_id = $1`, ""},
		{"deleted_at on an active item", `UPDATE media SET deleted_at = now() WHERE media_id = $1`, checkViolation},
		{"zero size", `UPDATE media SET size_bytes = 0 WHERE media_id = $1`, checkViolation},
		{"negative reactions", `UPDATE media SET reaction_count = -1 WHERE media_id = $1`, checkViolation},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			conn := migrated(t)
			id, err := insertMedia(conn, validMedia(newEvent(t, conn), nil))
			if err != nil {
				t.Fatalf("seed media: %v", err)
			}

			// Act
			_, err = conn.Exec(tc.update, id)

			// Assert
			if got := sqlState(t, err); got != tc.want {
				t.Fatalf("SQLSTATE = %q, want %q (err=%v)", got, tc.want, err)
			}
		})
	}
}

func TestMedia_hallOfFameRules(t *testing.T) {
	tests := []struct {
		name   string
		ready  bool
		update string
		want   string
	}{
		{"highlight a ready item", true, `UPDATE media SET hall_of_fame = true, hall_of_fame_rank = 1 WHERE media_id = $1`, ""},
		{"highlight without a rank", true, `UPDATE media SET hall_of_fame = true WHERE media_id = $1`, checkViolation},
		{"rank without a highlight", true, `UPDATE media SET hall_of_fame_rank = 1 WHERE media_id = $1`, checkViolation},
		{"rank zero", true, `UPDATE media SET hall_of_fame = true, hall_of_fame_rank = 0 WHERE media_id = $1`, checkViolation},
		{"highlight a pending item", false, `UPDATE media SET hall_of_fame = true, hall_of_fame_rank = 1 WHERE media_id = $1`, checkViolation},
		{"highlight a hidden item", true, `UPDATE media SET status = 'hidden', hall_of_fame = true, hall_of_fame_rank = 1 WHERE media_id = $1`, checkViolation},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			conn := migrated(t)
			id, err := insertMedia(conn, validMedia(newEvent(t, conn), nil))
			if err != nil {
				t.Fatalf("seed media: %v", err)
			}
			if tc.ready {
				markReady(t, conn, id)
			}

			// Act
			_, err = conn.Exec(tc.update, id)

			// Assert
			if got := sqlState(t, err); got != tc.want {
				t.Fatalf("SQLSTATE = %q, want %q (err=%v)", got, tc.want, err)
			}
		})
	}
}

func TestMedia_hidingAHighlightRequiresUnhighlightingFirst(t *testing.T) {
	// Arrange
	conn := migrated(t)
	id, err := insertMedia(conn, validMedia(newEvent(t, conn), nil))
	if err != nil {
		t.Fatalf("seed media: %v", err)
	}
	markReady(t, conn, id)
	if _, err := conn.Exec(`UPDATE media SET hall_of_fame = true, hall_of_fame_rank = 1 WHERE media_id = $1`, id); err != nil {
		t.Fatalf("highlight: %v", err)
	}

	// Act
	_, hideOnly := conn.Exec(`UPDATE media SET status = 'hidden' WHERE media_id = $1`, id)
	_, hideAndUnhighlight := conn.Exec(
		`UPDATE media SET status = 'hidden', hall_of_fame = false, hall_of_fame_rank = NULL WHERE media_id = $1`, id)

	// Assert
	if got := sqlState(t, hideOnly); got != checkViolation {
		t.Errorf("hide a highlight SQLSTATE = %q, want %q", got, checkViolation)
	}
	if hideAndUnhighlight != nil {
		t.Errorf("hide and un-highlight in one statement: %v", hideAndUnhighlight)
	}
}

func TestMedia_hallOfFameRankIsUniquePerEvent(t *testing.T) {
	// Arrange
	conn := migrated(t)
	eventA := newEvent(t, conn)
	eventB := newEvent(t, conn)
	ids := make([]string, 0, 3)
	for _, eventID := range []string{eventA, eventA, eventB} {
		id, err := insertMedia(conn, validMedia(eventID, nil))
		if err != nil {
			t.Fatalf("seed media: %v", err)
		}
		markReady(t, conn, id)
		ids = append(ids, id)
	}
	const highlight = `UPDATE media SET hall_of_fame = true, hall_of_fame_rank = 1 WHERE media_id = $1`
	if _, err := conn.Exec(highlight, ids[0]); err != nil {
		t.Fatalf("highlight first: %v", err)
	}

	// Act
	_, sameEventSameRank := conn.Exec(highlight, ids[1])
	_, otherEventSameRank := conn.Exec(highlight, ids[2])

	// Assert
	if got := sqlState(t, sameEventSameRank); got != uniqueViolation {
		t.Errorf("same rank in one event SQLSTATE = %q, want %q", got, uniqueViolation)
	}
	if otherEventSameRank != nil {
		t.Errorf("rank 1 in another event must be allowed: %v", otherEventSameRank)
	}
}

func TestMedia_guestScopeFilterSeesOnlyOwnItems(t *testing.T) {
	// FSD 3.4: guest queries filter by event_id AND session_id in SQL.
	// Arrange
	conn := migrated(t)
	eventID := newEvent(t, conn)
	mine := newGuestSession(t, conn, eventID)
	theirs := newGuestSession(t, conn, eventID)
	for _, session := range []string{mine, mine, theirs} {
		if _, err := insertMedia(conn, validMedia(eventID, session)); err != nil {
			t.Fatalf("seed media: %v", err)
		}
	}
	const scoped = `SELECT count(*) FROM media WHERE event_id = $1 AND session_id = $2 AND status <> 'deleted'`

	// Act
	var mineRows, theirRows int
	errMine := conn.QueryRow(scoped, eventID, mine).Scan(&mineRows)
	errTheirs := conn.QueryRow(scoped, eventID, theirs).Scan(&theirRows)

	// Assert
	if errMine != nil || errTheirs != nil {
		t.Fatalf("query: %v / %v", errMine, errTheirs)
	}
	if mineRows != 2 || theirRows != 1 {
		t.Fatalf("mine=%d theirs=%d, want 2 and 1", mineRows, theirRows)
	}
}

func TestMedia_galleryQueriesAreIndexed(t *testing.T) {
	want := []string{
		"(event_id, uploaded_at DESC, media_id DESC)",
		"(event_id, session_id, uploaded_at DESC)",
		"(event_id, hall_of_fame_rank)",
	}

	for _, columns := range want {
		t.Run(columns, func(t *testing.T) {
			// Arrange
			conn := migrated(t)

			// Act
			var indexDef string
			err := conn.QueryRow(
				`SELECT indexdef FROM pg_indexes
				 WHERE schemaname = 'public' AND tablename = 'media' AND position($1 in indexdef) > 0`, columns,
			).Scan(&indexDef)

			// Assert
			if err != nil {
				t.Fatalf("expected an index on media %s: %v", columns, err)
			}
		})
	}
}

func TestEvents_cannotBeDeletedWhileTheyHoldSessionsOrMedia(t *testing.T) {
	// Deletion must go through the retention job, which removes stored objects first.
	// Arrange
	conn := migrated(t)
	eventID := newEvent(t, conn)
	newGuestSession(t, conn, eventID)

	// Act
	_, err := conn.Exec(`DELETE FROM events WHERE event_id = $1`, eventID)

	// Assert
	if got := sqlState(t, err); got != foreignKeyViolation {
		t.Fatalf("SQLSTATE = %q, want %q", got, foreignKeyViolation)
	}
}
