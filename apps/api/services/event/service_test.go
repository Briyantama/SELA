package event_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Briyantama/SELA/internal/db"
	"github.com/Briyantama/SELA/internal/testdb"
	"github.com/Briyantama/SELA/services/event"
)

const baseURL = "https://sela.example.test"

var (
	base62Code  = regexp.MustCompile(`^[0-9A-Za-z]{8}$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func ptr[T any](v T) *T { return &v }

// codeQueue hands out predetermined short codes, then repeats the last one forever.
type codeQueue struct {
	mu    sync.Mutex
	codes []string
}

func (q *codeQueue) next() (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	code := q.codes[0]
	if len(q.codes) > 1 {
		q.codes = q.codes[1:]
	}
	return code, nil
}

type fixture struct {
	conn *sql.DB
	repo *event.PostgresRepository
	svc  *event.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	conn := testdb.New(t)
	if err := db.Up(context.Background(), conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := event.NewPostgresRepository(conn)
	return &fixture{conn: conn, repo: repo, svc: event.NewService(repo, event.Options{ShortLinkBaseURL: baseURL})}
}

// serviceWithCodes builds another service on the same database that yields the given short codes.
func (f *fixture) serviceWithCodes(codes ...string) *event.Service {
	return event.NewService(f.repo, event.Options{ShortLinkBaseURL: baseURL, NewShortCode: (&codeQueue{codes: codes}).next})
}

func (f *fixture) host(t *testing.T, email string) string {
	t.Helper()
	var id string
	if err := f.conn.QueryRow(`INSERT INTO hosts (email) VALUES ($1) RETURNING host_id`, email).Scan(&id); err != nil {
		t.Fatalf("insert host: %v", err)
	}
	return id
}

func birthday(name string) event.CreateInput {
	return event.CreateInput{CategoryCode: "ulang_tahun", Name: name, EventDate: "2026-12-05"}
}

func mustCreate(t *testing.T, svc *event.Service, hostID string, in event.CreateInput) event.Event {
	t.Helper()
	ev, err := svc.CreateEvent(context.Background(), hostID, in)
	if err != nil {
		t.Fatalf("CreateEvent(%+v): %v", in, err)
	}
	return ev
}

func TestListCategories_returnsTheSixPresetsInDisplayOrder(t *testing.T) {
	// Arrange
	f := newFixture(t)

	// Act
	cats, err := f.svc.ListCategories(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	var codes []string
	for _, c := range cats {
		codes = append(codes, c.Code)
		if c.Name == "" || c.ThemeKey == "" {
			t.Errorf("category %s has an empty name or theme", c.Code)
		}
	}
	want := "pernikahan,wisuda,ulang_tahun,gathering,reuni,lainnya"
	if got := strings.Join(codes, ","); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
}

func TestListCategories_resolvesUndefinedDefaultsToTheSafeFallbacks(t *testing.T) {
	tests := []struct {
		code         string
		rawReveal    string
		wantMode     string
		wantDelayHrs *int
	}{
		{"pernikahan", "delayed", "delayed", ptr(24)}, // defined by FSD 4.5
		{"wisuda", "instant", "instant", nil},         // defined by FSD 4.5
		{"gathering", "instant", "instant", nil},      // defined by FSD 4.5
		{"ulang_tahun", "tbd", "instant", nil},        // undefined: fallback
		{"reuni", "tbd", "instant", nil},              // undefined: fallback
		{"lainnya", "tbd", "instant", nil},            // undefined: fallback
	}

	// Arrange
	f := newFixture(t)
	cats, err := f.svc.ListCategories(context.Background())
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	byCode := map[string]event.Category{}
	for _, c := range cats {
		byCode[c.Code] = c
	}

	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			c := byCode[tc.code]

			// Assert
			if c.RevealDefault != tc.rawReveal {
				t.Errorf("raw reveal default = %q, want %q", c.RevealDefault, tc.rawReveal)
			}
			if c.ShotLimitDefault != "tbd" || c.DefaultShotLimit != nil {
				t.Errorf("raw shot default = %q/%v, want tbd with no number (nothing invented)", c.ShotLimitDefault, c.DefaultShotLimit)
			}
			if c.EffectiveShotLimit != nil {
				t.Errorf("effective shot limit = %d, want unlimited (nil) as the fallback", *c.EffectiveShotLimit)
			}
			if c.EffectiveRevealMode != tc.wantMode {
				t.Errorf("effective reveal mode = %q, want %q", c.EffectiveRevealMode, tc.wantMode)
			}
			if (c.EffectiveRevealDelayHours == nil) != (tc.wantDelayHrs == nil) ||
				(tc.wantDelayHrs != nil && *c.EffectiveRevealDelayHours != *tc.wantDelayHrs) {
				t.Errorf("effective delay hours = %v, want %v", c.EffectiveRevealDelayHours, tc.wantDelayHrs)
			}
		})
	}
}

func TestCreateEvent_aMinimalRequestGetsSafeDefaultsAndLinks(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "owner@example.test")

	// Act
	ev := mustCreate(t, f.svc, host, birthday("Ulang Tahun Sela"))

	// Assert
	if !uuidPattern.MatchString(ev.ID) {
		t.Errorf("id %q is not a UUID", ev.ID)
	}
	if ev.CategoryCode != "ulang_tahun" || ev.Name != "Ulang Tahun Sela" || ev.EventDate != "2026-12-05" {
		t.Errorf("event = %+v", ev)
	}
	if ev.Status != "active" || ev.Package != "gratis" || ev.Timezone != "Asia/Jakarta" {
		t.Errorf("defaults = status:%s package:%s tz:%s", ev.Status, ev.Package, ev.Timezone)
	}
	if ev.ShotLimit != nil || ev.RevealMode != "instant" || ev.RevealAt != nil {
		t.Errorf("settings = shot:%v reveal:%s at:%v, want unlimited/instant", ev.ShotLimit, ev.RevealMode, ev.RevealAt)
	}
	if !base62Code.MatchString(ev.ShortCode) {
		t.Errorf("short code %q is not 8 base62 characters", ev.ShortCode)
	}
	if ev.ShortLink != baseURL+"/"+ev.ShortCode {
		t.Errorf("short link = %q, want %q", ev.ShortLink, baseURL+"/"+ev.ShortCode)
	}
	if ev.QRPNGURL != "/api/v1/events/"+ev.ID+"/qr.png" || ev.QRSVGURL != "/api/v1/events/"+ev.ID+"/qr.svg" {
		t.Errorf("QR URLs = %q, %q", ev.QRPNGURL, ev.QRSVGURL)
	}
	if ev.CreatedAt.IsZero() {
		t.Error("created_at not set")
	}
	var owner string
	if err := f.conn.QueryRow(`SELECT host_id FROM events WHERE event_id = $1`, ev.ID).Scan(&owner); err != nil || owner != host {
		t.Errorf("stored owner = %q (%v), want the authenticated host %q", owner, err, host)
	}
}

func TestCreateEvent_aWeddingGetsItsDefinedDelayedReveal(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "owner@example.test")
	jakarta, _ := time.LoadLocation("Asia/Jakarta")

	// Act
	ev := mustCreate(t, f.svc, host, event.CreateInput{CategoryCode: "pernikahan", Name: "Akad", EventDate: "2026-12-05"})

	// Assert
	want := time.Date(2026, 12, 5, 0, 0, 0, 0, jakarta).Add(24 * time.Hour)
	if ev.RevealMode != "delayed" || ev.RevealAt == nil || !ev.RevealAt.Equal(want) {
		t.Fatalf("reveal = %s at %v, want delayed at %v (event date 00:00 WIB + 24h)", ev.RevealMode, ev.RevealAt, want)
	}
}

func TestCreateEvent_hostOverridesBeatCategoryDefaultsAndFallbacks(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	dayStart := time.Date(2026, 12, 5, 0, 0, 0, 0, jakarta)

	tests := []struct {
		name         string
		in           event.CreateInput
		wantShot     *int
		wantMode     string
		wantRevealAt *time.Time
	}{
		{"limit shots", event.CreateInput{CategoryCode: "ulang_tahun", ShotLimit: ptr(25)}, ptr(25), "instant", nil},
		{"explicit zero means unlimited", event.CreateInput{CategoryCode: "ulang_tahun", ShotLimit: ptr(0)}, nil, "instant", nil},
		{"instant beats the wedding default", event.CreateInput{CategoryCode: "pernikahan", RevealMode: ptr("instant")}, nil, "instant", nil},
		{"delayed with hours on an undefined category", event.CreateInput{CategoryCode: "ulang_tahun", RevealMode: ptr("delayed"), RevealDelayHours: ptr(48)}, nil, "delayed", ptr(dayStart.Add(48 * time.Hour))},
		{"hours alone adjust the wedding's delayed default", event.CreateInput{CategoryCode: "pernikahan", RevealDelayHours: ptr(6)}, nil, "delayed", ptr(dayStart.Add(6 * time.Hour))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			in := tc.in
			in.Name, in.EventDate = "Acara", "2026-12-05"

			// Act
			ev := mustCreate(t, f.svc, f.host(t, "owner@example.test"), in)

			// Assert
			if (ev.ShotLimit == nil) != (tc.wantShot == nil) || (tc.wantShot != nil && *ev.ShotLimit != *tc.wantShot) {
				t.Errorf("shot limit = %v, want %v", ev.ShotLimit, tc.wantShot)
			}
			if ev.RevealMode != tc.wantMode {
				t.Errorf("reveal mode = %q, want %q", ev.RevealMode, tc.wantMode)
			}
			if (ev.RevealAt == nil) != (tc.wantRevealAt == nil) || (tc.wantRevealAt != nil && !ev.RevealAt.Equal(*tc.wantRevealAt)) {
				t.Errorf("reveal at = %v, want %v", ev.RevealAt, tc.wantRevealAt)
			}
		})
	}
}

func TestCreateEvent_categoryIsAPresetNotAGate(t *testing.T) {
	// Every category must accept every combination of settings (FSD 4.1 business rules).
	f := newFixture(t)
	host := f.host(t, "owner@example.test")
	cats, err := f.svc.ListCategories(context.Background())
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}

	for _, c := range cats {
		for _, shots := range []*int{nil, ptr(5)} {
			for _, mode := range []string{"instant", "delayed"} {
				t.Run(fmt.Sprintf("%s/shots=%v/%s", c.Code, shots != nil, mode), func(t *testing.T) {
					in := event.CreateInput{CategoryCode: c.Code, Name: "Acara", EventDate: "2026-12-05", ShotLimit: shots, RevealMode: ptr(mode)}
					if mode == "delayed" {
						in.RevealDelayHours = ptr(12)
					}
					if _, err := f.svc.CreateEvent(context.Background(), host, in); err != nil {
						t.Fatalf("CreateEvent: %v", err)
					}
				})
			}
		}
	}
}

func TestCreateEvent_rejectsInvalidInputWithoutStoringAnything(t *testing.T) {
	tests := []struct {
		name    string
		in      event.CreateInput
		wantMsg string
	}{
		{"empty name", event.CreateInput{CategoryCode: "ulang_tahun", Name: "", EventDate: "2026-12-05"}, "name is required"},
		{"blank name", event.CreateInput{CategoryCode: "ulang_tahun", Name: "   ", EventDate: "2026-12-05"}, "name is required"},
		{"name too long", event.CreateInput{CategoryCode: "ulang_tahun", Name: strings.Repeat("a", 201), EventDate: "2026-12-05"}, "at most 200"},
		{"control characters", event.CreateInput{CategoryCode: "ulang_tahun", Name: "bad\x00name", EventDate: "2026-12-05"}, "invalid characters"},
		{"missing date", event.CreateInput{CategoryCode: "ulang_tahun", Name: "Acara"}, "event_date is required"},
		{"impossible date", event.CreateInput{CategoryCode: "ulang_tahun", Name: "Acara", EventDate: "2026-02-30"}, "valid date"},
		{"wrong date format", event.CreateInput{CategoryCode: "ulang_tahun", Name: "Acara", EventDate: "05/12/2026"}, "valid date"},
		{"unpadded date", event.CreateInput{CategoryCode: "ulang_tahun", Name: "Acara", EventDate: "2026-1-2"}, "valid date"},
		{"missing category", event.CreateInput{Name: "Acara", EventDate: "2026-12-05"}, "category_code is required"},
		{"unknown category", event.CreateInput{CategoryCode: "konser", Name: "Acara", EventDate: "2026-12-05"}, "unknown category"},
		{"negative shot limit", event.CreateInput{CategoryCode: "ulang_tahun", Name: "Acara", EventDate: "2026-12-05", ShotLimit: ptr(-1)}, "shot_limit"},
		{"unknown reveal mode", event.CreateInput{CategoryCode: "ulang_tahun", Name: "Acara", EventDate: "2026-12-05", RevealMode: ptr("later")}, "reveal_mode"},
		{"hours with an instant reveal", event.CreateInput{CategoryCode: "ulang_tahun", Name: "Acara", EventDate: "2026-12-05", RevealMode: ptr("instant"), RevealDelayHours: ptr(6)}, "only valid with a delayed reveal"},
		{"hours on a category that reveals instantly", event.CreateInput{CategoryCode: "wisuda", Name: "Acara", EventDate: "2026-12-05", RevealDelayHours: ptr(6)}, "only valid with a delayed reveal"},
		{"non-positive hours", event.CreateInput{CategoryCode: "ulang_tahun", Name: "Acara", EventDate: "2026-12-05", RevealMode: ptr("delayed"), RevealDelayHours: ptr(0)}, "must be positive"},
		{"delayed without hours where no default exists", event.CreateInput{CategoryCode: "ulang_tahun", Name: "Acara", EventDate: "2026-12-05", RevealMode: ptr("delayed")}, "required for a delayed reveal"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)

			// Act
			_, err := f.svc.CreateEvent(context.Background(), f.host(t, "owner@example.test"), tc.in)

			// Assert
			var ve *event.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want a *ValidationError", err)
			}
			if !strings.Contains(ve.Error(), tc.wantMsg) {
				t.Errorf("message %q should contain %q", ve.Error(), tc.wantMsg)
			}
			var rows int
			if err := f.conn.QueryRow(`SELECT count(*) FROM events`).Scan(&rows); err != nil || rows != 0 {
				t.Errorf("events stored = %d (%v), want 0", rows, err)
			}
		})
	}
}

func TestCreateEvent_trimsTheName(t *testing.T) {
	// Arrange
	f := newFixture(t)

	// Act
	ev := mustCreate(t, f.svc, f.host(t, "owner@example.test"), birthday("  Ulang Tahun  "))

	// Assert
	if ev.Name != "Ulang Tahun" {
		t.Errorf("name = %q, want it trimmed", ev.Name)
	}
}

func TestCreateEvent_retriesWhenTheShortCodeIsTaken(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "owner@example.test")
	first := mustCreate(t, f.serviceWithCodes("AAAAAAAA"), host, birthday("Pertama"))

	// Act: the generator first proposes the taken code, then a free one.
	second := mustCreate(t, f.serviceWithCodes("AAAAAAAA", "BBBBBBBB"), host, birthday("Kedua"))

	// Assert
	if first.ShortCode != "AAAAAAAA" || second.ShortCode != "BBBBBBBB" {
		t.Fatalf("codes = %q, %q; want AAAAAAAA then BBBBBBBB", first.ShortCode, second.ShortCode)
	}
}

func TestCreateEvent_givesUpWhenNoFreeShortCodeExists(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "owner@example.test")
	mustCreate(t, f.serviceWithCodes("AAAAAAAA"), host, birthday("Pertama"))

	// Act: the generator only ever proposes the taken code.
	_, err := f.serviceWithCodes("AAAAAAAA").CreateEvent(context.Background(), host, birthday("Kedua"))

	// Assert
	var ve *event.ValidationError
	if err == nil || errors.As(err, &ve) {
		t.Fatalf("err = %v, want an internal error, not a validation error", err)
	}
}

func TestCreateEvent_generatesDistinctShortCodesAndPrivateAccessTokens(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "owner@example.test")
	codes := map[string]bool{}
	tokens := map[string]bool{}

	// Act
	for i := range 20 {
		ev := mustCreate(t, f.svc, host, birthday(fmt.Sprintf("Acara %d", i)))
		codes[ev.ShortCode] = true
		var token string
		if err := f.conn.QueryRow(`SELECT access_token FROM events WHERE event_id = $1`, ev.ID).Scan(&token); err != nil {
			t.Fatalf("read token: %v", err)
		}
		if len(token) < 43 {
			t.Errorf("access token %q is too short to be 256 random bits", token)
		}
		tokens[token] = true
	}

	// Assert
	if len(codes) != 20 || len(tokens) != 20 {
		t.Errorf("distinct short codes = %d, tokens = %d, want 20 each", len(codes), len(tokens))
	}
}

func TestGetEvent_returnsTheOwnersEvent(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "owner@example.test")
	created := mustCreate(t, f.svc, host, event.CreateInput{CategoryCode: "pernikahan", Name: "Akad", EventDate: "2026-12-05", ShotLimit: ptr(10)})

	// Act
	got, err := f.svc.GetEvent(context.Background(), host, created.ID)

	// Assert
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if got.ID != created.ID || got.ShortLink != created.ShortLink || got.Name != "Akad" || got.EventDate != "2026-12-05" {
		t.Errorf("got %+v, want %+v", got, created)
	}
	if got.ShotLimit == nil || *got.ShotLimit != 10 || got.RevealMode != "delayed" || got.RevealAt == nil || !got.RevealAt.Equal(*created.RevealAt) {
		t.Errorf("settings changed on read: %+v", got)
	}
}

func TestGetEvent_isScopedToTheOwningHost(t *testing.T) {
	// Arrange
	f := newFixture(t)
	owner := f.host(t, "owner@example.test")
	stranger := f.host(t, "stranger@example.test")
	created := mustCreate(t, f.svc, owner, birthday("Pribadi"))

	tests := []struct{ name, host, id string }{
		{"another host", stranger, created.ID},
		{"unknown id", owner, "00000000-0000-4000-8000-000000000000"},
		{"malformed id", owner, "not-a-uuid"},
		{"empty id", owner, ""},
		{"injection attempt", owner, created.ID + "' OR '1'='1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, err := f.svc.GetEvent(context.Background(), tc.host, tc.id)

			// Assert
			if !errors.Is(err, event.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound (indistinguishable from a missing event)", err)
			}
		})
	}
}

func TestQRCode_rendersTheShortLinkForTheOwnerOnly(t *testing.T) {
	// Arrange
	f := newFixture(t)
	owner := f.host(t, "owner@example.test")
	stranger := f.host(t, "stranger@example.test")
	created := mustCreate(t, f.svc, owner, birthday("Pribadi"))

	// Act
	png, pngType, pngErr := f.svc.QRCode(context.Background(), owner, created.ID, event.QRPNG)
	svg, svgType, svgErr := f.svc.QRCode(context.Background(), owner, created.ID, event.QRSVG)
	_, _, strangerErr := f.svc.QRCode(context.Background(), stranger, created.ID, event.QRPNG)

	// Assert
	if pngErr != nil || svgErr != nil {
		t.Fatalf("QRCode: png=%v svg=%v", pngErr, svgErr)
	}
	if pngType != "image/png" || svgType != "image/svg+xml" {
		t.Errorf("content types = %q, %q", pngType, svgType)
	}
	img, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		t.Fatalf("PNG: %v", err)
	}
	if got := decodeQR(t, img); got != created.ShortLink {
		t.Errorf("PNG decodes to %q, want the event's short link %q", got, created.ShortLink)
	}
	if got := decodeQR(t, rasterizeSVG(t, string(svg))); got != created.ShortLink {
		t.Errorf("SVG decodes to %q, want %q", got, created.ShortLink)
	}
	if !errors.Is(strangerErr, event.ErrNotFound) {
		t.Errorf("another host's QR request err = %v, want ErrNotFound", strangerErr)
	}
}

func TestQRCode_rejectsAnUnsupportedFormat(t *testing.T) {
	// Arrange
	f := newFixture(t)
	owner := f.host(t, "owner@example.test")
	created := mustCreate(t, f.svc, owner, birthday("Pribadi"))

	// Act
	_, _, err := f.svc.QRCode(context.Background(), owner, created.ID, event.QRFormat("gif"))

	// Assert
	if !errors.Is(err, event.ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
}

func TestService_reportsDatabaseFailuresAsInternalErrors(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "owner@example.test")
	if err := f.conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ctx := context.Background()

	// Act
	_, listErr := f.svc.ListCategories(ctx)
	_, createErr := f.svc.CreateEvent(ctx, host, birthday("Acara"))
	_, getErr := f.svc.GetEvent(ctx, host, "00000000-0000-4000-8000-000000000000")

	// Assert
	for name, err := range map[string]error{"list": listErr, "create": createErr, "get": getErr} {
		var ve *event.ValidationError
		if err == nil || errors.As(err, &ve) || errors.Is(err, event.ErrNotFound) {
			t.Errorf("%s err = %v, want an internal error", name, err)
		}
	}
}

func TestNewService_requiresAShortLinkBaseURL(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewService without a base URL must panic: it is a deployment mistake")
		}
	}()

	event.NewService(nil, event.Options{})
}
