package media_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Briyantama/SELA/services/media"
)

const maxPhotoBytes = 20 << 20

func TestBeginUpload_issuesASignedURLAndAPendingRow(t *testing.T) {
	// Arrange
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{shotLimit: limit(3)})).Guest

	// Act
	up, err := f.svc.BeginUpload(context.Background(), g, "Image/JPEG; q=1", 1234)

	// Assert
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	if up.MediaID == "" || up.Request.Method != "PUT" || up.Request.Headers["Content-Type"] != "image/jpeg" ||
		up.Request.Headers["Content-Length"] != "1234" {
		t.Fatalf("upload = %+v", up)
	}
	if up.ShotsRemaining == nil || *up.ShotsRemaining != 2 {
		t.Fatalf("shots remaining = %v, want 2", up.ShotsRemaining)
	}
	if state := f.processingState(t, up.MediaID); state != "pending" {
		t.Fatalf("processing_state = %q, want pending", state)
	}
	if !strings.HasPrefix(f.store.KeyOf(up.Request), "incoming/") {
		t.Fatalf("upload key %q, want it under incoming/", f.store.KeyOf(up.Request))
	}
}

func TestBeginUpload_acceptsOnlyPhotosThisTaskCanStrip(t *testing.T) {
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{})).Guest

	tests := []struct {
		contentType string
		wantErr     error
	}{
		{"image/jpeg", nil},
		{"image/png", nil},
		{"image/webp", nil},
		{"image/heic", media.ErrUnsupportedType},
		{"video/mp4", media.ErrUnsupportedType},
		{"video/quicktime", media.ErrUnsupportedType},
		{"image/gif", media.ErrUnsupportedType},
		{"text/html", media.ErrUnsupportedType},
	}

	for _, tc := range tests {
		t.Run(tc.contentType, func(t *testing.T) {
			// Act
			_, err := f.svc.BeginUpload(context.Background(), g, tc.contentType, 100)

			// Assert
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestBeginUpload_enforcesTheSizeLimits(t *testing.T) {
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{})).Guest

	tests := []struct {
		name string
		size int64
		want func(error) bool
	}{
		{"exactly 20 MB", maxPhotoBytes, func(err error) bool { return err == nil }},
		{"one byte over", maxPhotoBytes + 1, func(err error) bool { return errors.Is(err, media.ErrTooLarge) }},
		{"zero", 0, isValidation("size_bytes")},
		{"negative", -1, isValidation("size_bytes")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, err := f.svc.BeginUpload(context.Background(), g, "image/png", tc.size)

			// Assert
			if !tc.want(err) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestBeginUpload_stopsWhenTheCameraIsFull(t *testing.T) {
	// Arrange
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{shotLimit: limit(2)})).Guest
	f.begin(t, g, 10)
	f.begin(t, g, 10)

	// Act
	_, err := f.svc.BeginUpload(context.Background(), g, "image/jpeg", 10)

	// Assert
	if !errors.Is(err, media.ErrQuotaExhausted) {
		t.Fatalf("err = %v, want ErrQuotaExhausted", err)
	}
	var rows int
	_ = f.conn.QueryRow(`SELECT count(*) FROM media WHERE session_id = $1`, g.SessionID).Scan(&rows)
	if rows != 2 {
		t.Fatalf("media rows = %d, want 2 (no row for the refused shot)", rows)
	}
}

func TestBeginUpload_quotaIsPerGuestSession(t *testing.T) {
	// Arrange
	f := newFixture(t)
	eventID := f.event(t, eventOpts{shotLimit: limit(1)})
	a := f.guest(t, eventID).Guest
	b := f.guest(t, eventID).Guest
	f.begin(t, a, 10)

	// Act
	_, err := f.svc.BeginUpload(context.Background(), b, "image/jpeg", 10)

	// Assert
	if err != nil {
		t.Fatalf("second guest's first shot: %v", err)
	}
}

func TestBeginUpload_neverOverspendsUnderConcurrency(t *testing.T) {
	// Arrange
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{shotLimit: limit(3)})).Guest
	const attempts = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, exhausted := 0, 0

	// Act
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.BeginUpload(context.Background(), g, "image/jpeg", 10)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, media.ErrQuotaExhausted):
				exhausted++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	// Assert
	if succeeded != 3 || exhausted != attempts-3 {
		t.Fatalf("succeeded %d, exhausted %d; want 3 and %d", succeeded, exhausted, attempts-3)
	}
}

func TestBeginUpload_unlimitedEventsNeverRunOut(t *testing.T) {
	// Arrange
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{})).Guest

	// Act / Assert
	for i := range 5 {
		up := f.begin(t, g, 10)
		if up.ShotsRemaining != nil {
			t.Fatalf("attempt %d: shots remaining = %v, want nil for unlimited", i, *up.ShotsRemaining)
		}
	}
	if keys := f.keysWithPrefix("quota:"); len(keys) != 0 {
		t.Fatalf("unlimited event created quota keys %v", keys)
	}
}

func TestBeginUpload_reseedsALostCounterFromTheDatabase(t *testing.T) {
	// Arrange: one finished shot out of two, then Redis loses the counter.
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{shotLimit: limit(2)})).Guest
	f.upload(t, g)
	for _, key := range f.keysWithPrefix("quota:") {
		f.mr.Del(key)
	}

	// Act
	_, first := f.svc.BeginUpload(context.Background(), g, "image/jpeg", 10)
	_, second := f.svc.BeginUpload(context.Background(), g, "image/jpeg", 10)

	// Assert
	if first != nil || !errors.Is(second, media.ErrQuotaExhausted) {
		t.Fatalf("after reseed: first %v, second %v; want one more shot then exhausted", first, second)
	}
}

func TestBeginUpload_refusesAnEventThatHasSinceEnded(t *testing.T) {
	// Arrange
	f := newFixture(t)
	eventID := f.event(t, eventOpts{})
	g := f.guest(t, eventID).Guest
	f.exec(t, `UPDATE events SET status = 'expired' WHERE event_id = $1`, eventID)

	// Act
	_, err := f.svc.BeginUpload(context.Background(), g, "image/jpeg", 10)

	// Assert
	if !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCompleteUpload_storesAStrippedCopyAndMarksItReady(t *testing.T) {
	// Arrange
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{shotLimit: limit(3)})).Guest
	body := jpegWithGPS(t)
	up := f.begin(t, g, len(body))
	if err := f.store.Upload(up.Request, "image/jpeg", body); err != nil {
		t.Fatalf("browser PUT: %v", err)
	}

	// Act
	item, err := f.svc.CompleteUpload(context.Background(), g, up.MediaID)

	// Assert
	if err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	if item.ID != up.MediaID || item.Processing != media.ProcessingReady || item.URL == "" || item.SizeBytes == nil {
		t.Fatalf("item = %+v", item)
	}
	keys := f.store.Keys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "media/") || keys[0] == f.store.KeyOf(up.Request) {
		t.Fatalf("stored keys = %v, want only the final media/ object", keys)
	}
	stored, err := f.store.Get(context.Background(), keys[0], maxPhotoBytes)
	if err != nil {
		t.Fatalf("read stored: %v", err)
	}
	if bytes.Contains(stored, []byte(gpsMarker)) || bytes.Contains(stored, []byte("Exif")) {
		t.Fatal("the stored photo still carries EXIF/GPS")
	}
	if int64(len(stored)) != *item.SizeBytes {
		t.Fatalf("size_bytes = %d, stored %d bytes", *item.SizeBytes, len(stored))
	}
}

func TestCompleteUpload_waitsForAnUploadThatHasNotArrived(t *testing.T) {
	// Arrange
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{})).Guest
	up := f.begin(t, g, 10)

	// Act
	_, err := f.svc.CompleteUpload(context.Background(), g, up.MediaID)

	// Assert
	if !errors.Is(err, media.ErrUploadMissing) {
		t.Fatalf("err = %v, want ErrUploadMissing", err)
	}
	if state := f.processingState(t, up.MediaID); state != "pending" {
		t.Fatalf("processing_state = %q, want still pending", state)
	}
}

func TestCompleteUpload_rejectsBadBytesAndReturnsTheShot(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name   string
		body   func([]byte) []byte
		signed func(int) int
	}{
		{"png bytes declared as jpeg", func([]byte) []byte { return []byte("\x89PNG\r\n\x1a\nnot really") }, nil},
		{"html declared as jpeg", func([]byte) []byte { return []byte("<script>alert(1)</script>") }, nil},
		{"truncated jpeg", func(b []byte) []byte { return b[:len(b)/2] }, nil},
		{"size differs from the declared size", func(b []byte) []byte { return b }, func(n int) int { return n + 1 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: a one-shot camera, so a returned shot is observable.
			g := f.guest(t, f.event(t, eventOpts{shotLimit: limit(1)})).Guest
			body := tc.body(jpegWithGPS(t))
			declared := len(body)
			if tc.signed != nil {
				declared = tc.signed(len(body))
			}
			up := f.begin(t, g, declared)
			if err := f.store.Put(context.Background(), f.store.KeyOf(up.Request), "image/jpeg", body); err != nil {
				t.Fatalf("put: %v", err)
			}

			// Act
			_, err := f.svc.CompleteUpload(context.Background(), g, up.MediaID)

			// Assert
			if !errors.Is(err, media.ErrRejected) {
				t.Fatalf("err = %v, want ErrRejected", err)
			}
			if state := f.processingState(t, up.MediaID); state != "failed" {
				t.Fatalf("processing_state = %q, want failed", state)
			}
			if _, err := f.svc.BeginUpload(context.Background(), g, "image/jpeg", 10); err != nil {
				t.Fatalf("the rejected shot was not returned: %v", err)
			}
			for _, key := range f.store.Keys() {
				if key == f.store.KeyOf(up.Request) {
					t.Fatal("the rejected upload was left in storage")
				}
			}
		})
	}
}

func TestCompleteUpload_onlyTheUploaderCanCompleteAPendingItemOnce(t *testing.T) {
	// Arrange
	f := newFixture(t)
	eventID := f.event(t, eventOpts{})
	owner := f.guest(t, eventID).Guest
	other := f.guest(t, eventID).Guest
	body := jpegWithGPS(t)
	up := f.begin(t, owner, len(body))
	if err := f.store.Upload(up.Request, "image/jpeg", body); err != nil {
		t.Fatalf("browser PUT: %v", err)
	}
	ctx := context.Background()

	// Act
	_, byOther := f.svc.CompleteUpload(ctx, other, up.MediaID)
	_, byOwner := f.svc.CompleteUpload(ctx, owner, up.MediaID)
	_, twice := f.svc.CompleteUpload(ctx, owner, up.MediaID)
	_, bogus := f.svc.CompleteUpload(ctx, owner, "not-a-uuid")

	// Assert
	if !errors.Is(byOther, media.ErrNotFound) {
		t.Fatalf("another guest: err = %v, want ErrNotFound", byOther)
	}
	if byOwner != nil {
		t.Fatalf("owner: %v", byOwner)
	}
	if !errors.Is(twice, media.ErrNotFound) || !errors.Is(bogus, media.ErrNotFound) {
		t.Fatalf("second completion %v, bogus id %v; want ErrNotFound", twice, bogus)
	}
}

func TestSweepExpired_failsAbandonedUploadsAndReturnsTheirShots(t *testing.T) {
	// Arrange
	f := newFixture(t)
	g := f.guest(t, f.event(t, eventOpts{shotLimit: limit(1)})).Guest
	abandoned := f.begin(t, g, 10)
	if err := f.store.Upload(abandoned.Request, "image/jpeg", make([]byte, 10)); err != nil {
		t.Fatalf("put: %v", err)
	}
	ctx := context.Background()

	// Act
	early, earlyErr := f.svc.SweepExpired(ctx)
	f.clock.Advance(uploadTTL + 6*time.Minute)
	swept, err := f.svc.SweepExpired(ctx)
	again, _ := f.svc.SweepExpired(ctx)

	// Assert
	if earlyErr != nil || early != 0 {
		t.Fatalf("sweep before the deadline = %d, %v; want 0", early, earlyErr)
	}
	if err != nil || swept != 1 || again != 0 {
		t.Fatalf("sweep = %d, %v, then %d; want 1 then 0", swept, err, again)
	}
	if state := f.processingState(t, abandoned.MediaID); state != "failed" {
		t.Fatalf("processing_state = %q, want failed", state)
	}
	if len(f.store.Keys()) != 0 {
		t.Fatalf("storage still holds %v", f.store.Keys())
	}
	if _, err := f.svc.BeginUpload(ctx, g, "image/jpeg", 10); err != nil {
		t.Fatalf("the abandoned shot was not returned: %v", err)
	}
}

func isValidation(field string) func(error) bool {
	return func(err error) bool {
		var verr *media.ValidationError
		return errors.As(err, &verr) && verr.Field == field
	}
}
