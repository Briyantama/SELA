package media_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Briyantama/SELA/internal/db"
	"github.com/Briyantama/SELA/internal/objstore"
	"github.com/Briyantama/SELA/internal/testdb"
	"github.com/Briyantama/SELA/services/media"
)

const (
	testHMACKey = "0123456789abcdef0123456789abcdef-media-test"
	gpsMarker   = "GPS-SECRET-6.2088S-106.8456E"
	sessionTTL  = 72 * time.Hour
	uploadTTL   = 10 * time.Minute
)

var seq atomic.Int64

// clock is a settable time source shared by the service and the tests.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fixture struct {
	conn  *sql.DB
	mr    *miniredis.Miniredis
	store *objstore.Memory
	clock *clock
	svc   *media.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	conn := testdb.New(t)
	if err := db.Up(context.Background(), conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	f := &fixture{
		conn:  conn,
		mr:    mr,
		store: objstore.NewMemory(),
		clock: &clock{now: time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)},
	}
	svc, err := media.NewService(media.NewPostgresRepository(conn), media.NewRedisGuests(rdb), f.store, media.Options{
		HMACKey:    []byte(testHMACKey),
		SessionTTL: sessionTTL,
		UploadTTL:  uploadTTL,
		Now:        f.clock.Now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	f.svc = svc
	return f
}

func (f *fixture) host(t *testing.T) string {
	t.Helper()
	var id string
	err := f.conn.QueryRow(`INSERT INTO hosts (email) VALUES ($1) RETURNING host_id`,
		fmt.Sprintf("media-host%d@example.test", seq.Add(1))).Scan(&id)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	return id
}

type eventOpts struct {
	hostID         string
	shotLimit      *int
	revealMode     string // default instant
	revealAt       *time.Time
	status         string // default active
	expiresAt      *time.Time
	involvesMinors bool
}

// event inserts an event and returns its id. Use eventWithCode when the test needs the short code.
func (f *fixture) event(t *testing.T, o eventOpts) string {
	t.Helper()
	id, _ := f.eventWithCode(t, o)
	return id
}

// eventWithCode inserts an event and returns its id and its generated short code.
func (f *fixture) eventWithCode(t *testing.T, o eventOpts) (string, string) {
	t.Helper()
	if o.hostID == "" {
		o.hostID = f.host(t)
	}
	if o.revealMode == "" {
		o.revealMode = "instant"
	}
	if o.status == "" {
		o.status = "active"
	}
	n := seq.Add(1)
	code := fmt.Sprintf("Md%06d", n%1000000)
	var id string
	err := f.conn.QueryRow(
		`INSERT INTO events (host_id, category_code, name, event_date, access_token, short_code,
		                     shot_limit, reveal_mode, reveal_at, status, expires_at, involves_minors)
		 VALUES ($1, 'ulang_tahun', 'Pesta Sela', '2026-12-01', $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING event_id`,
		o.hostID, fmt.Sprintf("media-token-%020d-abcdefghijklmnop", n), code,
		o.shotLimit, o.revealMode, o.revealAt, o.status, o.expiresAt, o.involvesMinors,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return id, code
}

func limit(n int) *int { return &n }

func (f *fixture) guest(t *testing.T, eventID string) media.StartedSession {
	t.Helper()
	started, err := f.svc.StartSession(context.Background(), eventID, "", nil)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return started
}

// begin asks for an upload URL for a JPEG of the given size.
func (f *fixture) begin(t *testing.T, g media.Guest, size int) media.Upload {
	t.Helper()
	up, err := f.svc.BeginUpload(context.Background(), g, "image/jpeg", int64(size))
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	return up
}

// upload runs the whole flow: URL, browser PUT, completion.
func (f *fixture) upload(t *testing.T, g media.Guest) media.Item {
	t.Helper()
	body := jpegWithGPS(t)
	up := f.begin(t, g, len(body))
	if err := f.store.Upload(up.Request, "image/jpeg", body); err != nil {
		t.Fatalf("browser PUT: %v", err)
	}
	item, err := f.svc.CompleteUpload(context.Background(), g, up.MediaID)
	if err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	return item
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *fixture) processingState(t *testing.T, mediaID string) string {
	t.Helper()
	var state string
	if err := f.conn.QueryRow(`SELECT processing_state FROM media WHERE media_id = $1`, mediaID).Scan(&state); err != nil {
		t.Fatalf("read media: %v", err)
	}
	return state
}

func (f *fixture) keysWithPrefix(prefix string) []string {
	var keys []string
	for _, key := range f.mr.Keys() {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}

// jpegWithGPS is a small JPEG carrying an EXIF block with gpsMarker inside.
func jpegWithGPS(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	img.Set(0, 0, color.White)
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	plain := enc.Bytes()
	payload := append([]byte("Exif\x00\x00II*\x00\x08\x00\x00\x00\x00\x00\x00\x00\x00\x00"), gpsMarker...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))

	var out bytes.Buffer
	out.Write(plain[:2])
	out.Write(seg)
	out.Write(payload)
	out.Write(plain[2:])
	return out.Bytes()
}
