package event_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Briyantama/SELA/services/auth"
	"github.com/Briyantama/SELA/services/event"
)

// resolvePath is the public short-link resolver. It mirrors the guest-facing /e/{short_code} link
// under /api/v1 so the web app can own /e/{short_code} itself for the guest PWA page. It cannot nest
// under /api/v1/events/: ServeMux rejects any two-segment pattern there as conflicting with
// {id}/qr.png. The human-facing short link keeps its /e/ form.
const resolvePath = "/api/v1/e/"

// fakeAuth resolves session tokens to host ids, standing in for the real sign-in flow.
type fakeAuth struct {
	tokens map[string]string
	err    error
}

func (fakeAuth) RequestOTP(context.Context, string, string) (auth.RequestResult, error) {
	return auth.RequestResult{}, nil
}

func (fakeAuth) VerifyOTP(context.Context, string, string) (auth.Session, error) {
	return auth.Session{}, nil
}

func (a fakeAuth) Authenticate(_ context.Context, token string) (string, error) {
	if a.err != nil {
		return "", a.err
	}
	if id, ok := a.tokens[token]; ok {
		return id, nil
	}
	return "", auth.ErrUnauthenticated
}

// stubEvents forces specific use-case outcomes.
type stubEvents struct{ err error }

func (s stubEvents) ListCategories(context.Context) ([]event.Category, error) { return nil, s.err }
func (s stubEvents) CreateEvent(context.Context, string, event.CreateInput) (event.Event, error) {
	return event.Event{}, s.err
}
func (s stubEvents) GetEvent(context.Context, string, string) (event.Event, error) {
	return event.Event{}, s.err
}
func (s stubEvents) QRCode(context.Context, string, string, event.QRFormat) ([]byte, string, error) {
	return nil, "", s.err
}
func (s stubEvents) ResolveShortCode(context.Context, string) (event.PublicEvent, error) {
	return event.PublicEvent{}, s.err
}

// stubPermissionGuard answers every RequirePermission check the same way, regardless of the code.
type stubPermissionGuard struct{ allow bool }

func (g stubPermissionGuard) RequirePermission(string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !g.allow {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func newMuxWithPermission(svc event.Events, tokens map[string]string, allow bool) *http.ServeMux {
	guard := auth.NewHTTPHandler(fakeAuth{tokens: tokens}, auth.HTTPConfig{})
	mux := http.NewServeMux()
	event.NewHTTPHandler(svc, guard, stubPermissionGuard{allow: allow}).Register(mux)
	return mux
}

func newMux(svc event.Events, tokens map[string]string) *http.ServeMux {
	return newMuxWithPermission(svc, tokens, true)
}

func do(mux http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "sela_session", Value: token})
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// doJSON always sends a JSON content type, even for an empty body.
func doJSON(mux http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "sela_session", Value: token})
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type envelope[T any] struct {
	Success bool    `json:"success"`
	Data    T       `json:"data"`
	Error   *string `json:"error"`
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) envelope[T] {
	t.Helper()
	var env envelope[T]
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("not a JSON envelope: %v (%q)", err, rec.Body.String())
	}
	return env
}

type eventJSON struct {
	EventID      string  `json:"event_id"`
	CategoryCode string  `json:"category_code"`
	Name         string  `json:"name"`
	EventDate    string  `json:"event_date"`
	Timezone     string  `json:"timezone"`
	Status       string  `json:"status"`
	ShortCode    string  `json:"short_code"`
	ShortLink    string  `json:"short_link"`
	QRPNGURL     string  `json:"qr_png_url"`
	QRSVGURL     string  `json:"qr_svg_url"`
	ShotLimit    *int    `json:"shot_limit"`
	RevealMode   string  `json:"reveal_mode"`
	RevealAt     *string `json:"reveal_at"`
	Package      string  `json:"package"`
	CreatedAt    string  `json:"created_at"`
}

type httpFixture struct {
	*fixture
	mux   *http.ServeMux
	owner string
	other string
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	f := newFixture(t)
	owner, other := f.host(t, "owner@example.test"), f.host(t, "other@example.test")
	return &httpFixture{fixture: f, owner: owner, other: other,
		mux: newMux(f.svc, map[string]string{"owner-token": owner, "other-token": other})}
}

func (h *httpFixture) createBirthday(t *testing.T) eventJSON {
	t.Helper()
	rec := do(h.mux, http.MethodPost, "/api/v1/events", "owner-token", `{"category_code":"ulang_tahun","name":"Ulang Tahun","event_date":"2026-12-05"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[eventJSON](t, rec).Data
}

func TestHTTPCategories_arePublicPresetsWithEffectiveDefaults(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)

	// Act
	rec := do(h.mux, http.MethodGet, "/api/v1/event-categories", "", "")

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	type category struct {
		Code                      string `json:"code"`
		Name                      string `json:"name"`
		ThemeKey                  string `json:"theme_key"`
		ShotLimitDefault          string `json:"shot_limit_default"`
		DefaultShotLimit          *int   `json:"default_shot_limit"`
		RevealDefault             string `json:"reveal_default"`
		DefaultRevealDelayHours   *int   `json:"default_reveal_delay_hours"`
		EffectiveShotLimit        *int   `json:"effective_shot_limit"`
		EffectiveRevealMode       string `json:"effective_reveal_mode"`
		EffectiveRevealDelayHours *int   `json:"effective_reveal_delay_hours"`
	}
	env := decode[struct {
		Categories []category `json:"categories"`
	}](t, rec)
	if !env.Success || len(env.Data.Categories) != 6 {
		t.Fatalf("categories = %+v", env)
	}
	wedding, birthday := env.Data.Categories[0], env.Data.Categories[2]
	if wedding.Code != "pernikahan" || wedding.EffectiveRevealMode != "delayed" || wedding.EffectiveRevealDelayHours == nil || *wedding.EffectiveRevealDelayHours != 24 {
		t.Errorf("wedding = %+v", wedding)
	}
	if birthday.Code != "ulang_tahun" || birthday.RevealDefault != "tbd" || birthday.EffectiveRevealMode != "instant" || birthday.EffectiveShotLimit != nil {
		t.Errorf("birthday = %+v, want the tbd default resolved to instant/unlimited", birthday)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestHTTPCreateEvent_createsForTheAuthenticatedHost(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)

	// Act
	rec := do(h.mux, http.MethodPost, "/api/v1/events", "owner-token",
		`{"category_code":"pernikahan","name":"  Akad Nikah ","event_date":"2026-12-05","shot_limit":30}`)

	// Assert
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	got := decode[eventJSON](t, rec).Data
	if got.Name != "Akad Nikah" || got.CategoryCode != "pernikahan" || got.EventDate != "2026-12-05" {
		t.Errorf("event = %+v", got)
	}
	if got.ShotLimit == nil || *got.ShotLimit != 30 || got.RevealMode != "delayed" || got.RevealAt == nil {
		t.Errorf("settings = shot:%v reveal:%s at:%v", got.ShotLimit, got.RevealMode, got.RevealAt)
	}
	if got.ShortLink != baseURL+"/e/"+got.ShortCode || got.QRPNGURL != "/api/v1/events/"+got.EventID+"/qr.png" {
		t.Errorf("links = %q, %q", got.ShortLink, got.QRPNGURL)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/events/"+got.EventID {
		t.Errorf("Location = %q", loc)
	}
	body := rec.Body.String()
	for _, secret := range []string{"host_id", "access_token", h.owner} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaks %q: %s", secret, body)
		}
	}
	var owner string
	if err := h.conn.QueryRow(`SELECT host_id FROM events WHERE event_id = $1`, got.EventID).Scan(&owner); err != nil || owner != h.owner {
		t.Errorf("stored owner = %q (%v), want %q", owner, err, h.owner)
	}
}

func TestHTTPCreateEvent_requiresTheEventsCreatePermission(t *testing.T) {
	// Arrange
	f := newFixture(t)
	owner := f.host(t, "owner@example.test")
	mux := newMuxWithPermission(f.svc, map[string]string{"owner-token": owner}, false)

	// Act
	rec := do(mux, http.MethodPost, "/api/v1/events", "owner-token",
		`{"category_code":"ulang_tahun","name":"Ulang Tahun","event_date":"2026-12-05"}`)

	// Assert
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHTTPCreateEvent_neverTrustsAHostIdInTheBody(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)

	// Act
	rec := do(h.mux, http.MethodPost, "/api/v1/events", "owner-token",
		`{"category_code":"ulang_tahun","name":"Acara","event_date":"2026-12-05","host_id":"`+h.other+`"}`)

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (unknown field), body %s", rec.Code, rec.Body)
	}
	var rows int
	if err := h.conn.QueryRow(`SELECT count(*) FROM events`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("events stored = %d (%v), want 0", rows, err)
	}
}

func TestHTTPCreateEvent_rejectsBadRequestsWithClearMessages(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"missing name", `{"category_code":"ulang_tahun","event_date":"2026-12-05"}`, "name is required"},
		{"bad date", `{"category_code":"ulang_tahun","name":"A","event_date":"2026-13-40"}`, "valid date"},
		{"unknown category", `{"category_code":"konser","name":"A","event_date":"2026-12-05"}`, "unknown category"},
		{"negative shot limit", `{"category_code":"ulang_tahun","name":"A","event_date":"2026-12-05","shot_limit":-2}`, "shot_limit"},
		{"delay without delayed reveal", `{"category_code":"ulang_tahun","name":"A","event_date":"2026-12-05","reveal_delay_hours":3}`, "only valid with a delayed reveal"},
		{"wrong field type", `{"category_code":"ulang_tahun","name":"A","event_date":"2026-12-05","shot_limit":"ten"}`, "invalid request body"},
		{"malformed json", `{"category_code":`, "invalid request body"},
		{"empty body", ``, "request body is empty"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h := newHTTPFixture(t)

			// Act
			rec := doJSON(h.mux, http.MethodPost, "/api/v1/events", "owner-token", tc.body)

			// Assert
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Fatalf("status = %d body = %s, want 400 containing %q", rec.Code, rec.Body, tc.wantMsg)
			}
		})
	}
}

func TestHTTPProtectedRoutes_requireASession(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)
	created := h.createBirthday(t)

	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/events", `{"category_code":"ulang_tahun","name":"A","event_date":"2026-12-05"}`},
		{http.MethodGet, "/api/v1/events/" + created.EventID, ""},
		{http.MethodGet, "/api/v1/events/" + created.EventID + "/qr.png", ""},
		{http.MethodGet, "/api/v1/events/" + created.EventID + "/qr.svg", ""},
	}

	for _, r := range routes {
		for name, token := range map[string]string{"no cookie": "", "unknown session": "nope"} {
			t.Run(r.method+" "+r.path+" "+name, func(t *testing.T) {
				// Act
				rec := do(h.mux, r.method, r.path, token, r.body)

				// Assert
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", rec.Code)
				}
			})
		}
	}
	var rows int
	if err := h.conn.QueryRow(`SELECT count(*) FROM events`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("events stored = %d (%v): an unauthenticated create must not store anything", rows, err)
	}
}

func TestHTTPGetEvent_returnsOnlyTheOwnersEvent(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)
	created := h.createBirthday(t)

	// Act
	owner := do(h.mux, http.MethodGet, "/api/v1/events/"+created.EventID, "owner-token", "")
	other := do(h.mux, http.MethodGet, "/api/v1/events/"+created.EventID, "other-token", "")
	malformed := do(h.mux, http.MethodGet, "/api/v1/events/not-a-uuid", "owner-token", "")

	// Assert
	if owner.Code != http.StatusOK {
		t.Fatalf("owner status = %d, body %s", owner.Code, owner.Body)
	}
	if got := decode[eventJSON](t, owner).Data; got.EventID != created.EventID || got.ShortLink != created.ShortLink {
		t.Errorf("owner got %+v, want %+v", got, created)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{"other host": other, "malformed id": malformed} {
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", name, rec.Code)
		}
		if env := decode[any](t, rec); env.Error == nil || *env.Error != "event not found" {
			t.Errorf("%s body = %s", name, rec.Body)
		}
		if strings.Contains(rec.Body.String(), created.Name) {
			t.Errorf("%s response leaks event data: %s", name, rec.Body)
		}
	}
}

func TestHTTPQRCodes_decodeToTheShortLinkForTheOwnerOnly(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)
	created := h.createBirthday(t)

	// Act
	png := do(h.mux, http.MethodGet, created.QRPNGURL, "owner-token", "")
	svg := do(h.mux, http.MethodGet, created.QRSVGURL, "owner-token", "")
	other := do(h.mux, http.MethodGet, created.QRPNGURL, "other-token", "")

	// Assert
	if png.Code != http.StatusOK || png.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("png = %d %q", png.Code, png.Header().Get("Content-Type"))
	}
	img, _, err := image.Decode(bytes.NewReader(png.Body.Bytes()))
	if err != nil {
		t.Fatalf("png body is not an image: %v", err)
	}
	if got := decodeQR(t, img); got != created.ShortLink {
		t.Errorf("PNG decodes to %q, want %q", got, created.ShortLink)
	}
	if svg.Code != http.StatusOK || svg.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("svg = %d %q", svg.Code, svg.Header().Get("Content-Type"))
	}
	if got := decodeQR(t, rasterizeSVG(t, svg.Body.String())); got != created.ShortLink {
		t.Errorf("SVG decodes to %q, want %q", got, created.ShortLink)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{"png": png, "svg": svg} {
		if rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Robots-Tag") != "noindex" {
			t.Errorf("%s headers = %v", name, rec.Header())
		}
	}
	if other.Code != http.StatusNotFound || other.Header().Get("Content-Type") != "application/json" {
		t.Errorf("another host's QR request = %d %q, want a JSON 404", other.Code, other.Header().Get("Content-Type"))
	}
}

func TestHTTPRoutes_rejectUnsupportedMethods(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)

	tests := []struct{ method, path string }{
		{http.MethodPut, "/api/v1/events"},
		{http.MethodDelete, "/api/v1/events/00000000-0000-4000-8000-000000000000"},
		{http.MethodPost, "/api/v1/event-categories"},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			// Act
			rec := do(h.mux, tc.method, tc.path, "owner-token", "")

			// Assert
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
		})
	}
}

type publicEventJSON struct {
	EventID      string  `json:"event_id"`
	ShortCode    string  `json:"short_code"`
	Name         string  `json:"name"`
	EventDate    string  `json:"event_date"`
	Timezone     string  `json:"timezone"`
	CategoryCode string  `json:"category_code"`
	ThemeKey     string  `json:"theme_key"`
	Status       string  `json:"status"`
	ShotLimit    *int    `json:"shot_limit"`
	RevealMode   string  `json:"reveal_mode"`
	RevealAt     *string `json:"reveal_at"`
}

func TestHTTPResolve_isPublicAndReturnsGuestSafeMetadata(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)
	created := h.createBirthday(t)

	for name, token := range map[string]string{"no session": "", "an invalid session": "nope"} {
		t.Run(name, func(t *testing.T) {
			// Act
			rec := do(h.mux, http.MethodGet, resolvePath+created.ShortCode, token, "")

			// Assert
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}
			got := decode[publicEventJSON](t, rec).Data
			if got.EventID != created.EventID || got.ShortCode != created.ShortCode || got.Name != "Ulang Tahun" || got.EventDate != "2026-12-05" {
				t.Errorf("got %+v, want the created event", got)
			}
			if got.Timezone != "Asia/Jakarta" || got.CategoryCode != "ulang_tahun" || got.ThemeKey != "birthday" || got.Status != "active" {
				t.Errorf("got %+v", got)
			}
			if got.ShotLimit != nil || got.RevealMode != "instant" || got.RevealAt != nil {
				t.Errorf("settings = shot:%v reveal:%s at:%v", got.ShotLimit, got.RevealMode, got.RevealAt)
			}
			body := rec.Body.String()
			for _, private := range []string{"host_id", "access_token", "package", "created_at", "qr_png_url", "qr_svg_url", h.owner} {
				if strings.Contains(body, private) {
					t.Errorf("public response leaks %q: %s", private, body)
				}
			}
		})
	}
}

func TestHTTPResolve_noLongerServesTheGuestPWAPath(t *testing.T) {
	// The /e/{short_code} path belongs to the web app's guest page: the API must not answer there,
	// or the two collide on one URL. The short link itself keeps the /e/ form (see the create tests).
	// Arrange
	h := newHTTPFixture(t)
	created := h.createBirthday(t)

	// Act
	rec := do(h.mux, http.MethodGet, "/e/"+created.ShortCode, "", "")

	// Assert
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /e/%s = %d, want 404 from the mux; body %s", created.ShortCode, rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), created.EventID) {
		t.Errorf("the retired path still resolved the event: %s", rec.Body)
	}
}

func TestHTTPResolve_setsSafeHeaders(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)
	created := h.createBirthday(t)

	// Act
	rec := do(h.mux, http.MethodGet, resolvePath+created.ShortCode, "", "")

	// Assert
	want := map[string]string{
		"Content-Type":           "application/json",
		"Cache-Control":          "no-store",
		"X-Robots-Tag":           "noindex",
		"X-Content-Type-Options": "nosniff",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
}

func TestHTTPResolve_everyFailureLooksIdentical(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)
	created := h.createBirthday(t)
	draft := h.createBirthday(t)
	if _, err := h.conn.Exec(`UPDATE events SET status = 'draft' WHERE event_id = $1`, draft.EventID); err != nil {
		t.Fatalf("set status: %v", err)
	}
	expired := h.createBirthday(t)
	if _, err := h.conn.Exec(`UPDATE events SET status = 'expired' WHERE event_id = $1`, expired.EventID); err != nil {
		t.Fatalf("set status: %v", err)
	}
	pastExpiry := h.createBirthday(t)
	if _, err := h.conn.Exec(`UPDATE events SET expires_at = now() - interval '1 hour' WHERE event_id = $1`, pastExpiry.EventID); err != nil {
		t.Fatalf("set expiry: %v", err)
	}
	baseline := do(h.mux, http.MethodGet, resolvePath+"ZZZZZZZZ", "", "")

	tests := map[string]string{
		"unknown code":      "ZZZZZZZZ",
		"wrong case":        strings.ToLower(created.ShortCode) + "x", // still 9 chars: malformed
		"too short":         "abc",
		"too long":          "ABCDEFGHIJKL",
		"injection":         "%27%20OR%20%271%27%3D%271",
		"draft event":       draft.ShortCode,
		"expired event":     expired.ShortCode,
		"past expiry":       pastExpiry.ShortCode,
		"unicode":           "%C3%85%C3%85%C3%85%C3%85%C3%85%C3%85%C3%85%C3%85",
		"encoded null byte": "AAAAAAA%00",
		"path traversal":    "..%2F..%2F..%2F",
	}

	// Assert the baseline itself is the clean 404 envelope.
	if baseline.Code != http.StatusNotFound || decode[any](t, baseline).Error == nil || *decode[any](t, baseline).Error != "event not found" {
		t.Fatalf("baseline = %d %s", baseline.Code, baseline.Body)
	}

	for name, code := range tests {
		t.Run(name, func(t *testing.T) {
			// Act
			rec := do(h.mux, http.MethodGet, resolvePath+code, "", "")

			// Assert
			if rec.Code != http.StatusNotFound || rec.Body.String() != baseline.Body.String() {
				t.Fatalf("got %d %q, want the identical 404 %q", rec.Code, rec.Body, baseline.Body)
			}
		})
	}
}

func TestHTTPResolve_onlyAcceptsGet(t *testing.T) {
	// Arrange
	h := newHTTPFixture(t)
	created := h.createBirthday(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			// Act
			rec := do(h.mux, method, resolvePath+created.ShortCode, "owner-token", "")

			// Assert
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
		})
	}
}

func TestHTTPResolve_mapsErrorsWithoutLeakingDetails(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{"not found", event.ErrNotFound, http.StatusNotFound, "event not found"},
		{"internal", errors.New("pq: password authentication failed for user sela at 10.0.0.5"), http.StatusInternalServerError, "internal error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			mux := newMux(stubEvents{err: tc.err}, nil)

			// Act
			rec := do(mux, http.MethodGet, resolvePath+"AbCdEfGh", "", "")

			// Assert
			if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Fatalf("status = %d body = %s, want %d containing %q", rec.Code, rec.Body, tc.wantStatus, tc.wantMsg)
			}
			if strings.Contains(rec.Body.String(), "password") || strings.Contains(rec.Body.String(), "10.0.0.5") {
				t.Errorf("response leaks internal detail: %s", rec.Body)
			}
		})
	}
}

func TestHTTPErrors_mapToStatusCodesWithoutLeakingDetails(t *testing.T) {
	const eventPath = "/api/v1/events/00000000-0000-4000-8000-000000000000"
	createBody := `{"category_code":"ulang_tahun","name":"A","event_date":"2026-12-05"}`

	tests := []struct {
		name       string
		err        error
		method     string
		path       string
		body       string
		wantStatus int
		wantMsg    string
	}{
		{"validation on create", &event.ValidationError{Message: "name is required"}, http.MethodPost, "/api/v1/events", createBody, http.StatusBadRequest, "name is required"},
		{"not found on get", event.ErrNotFound, http.MethodGet, eventPath, "", http.StatusNotFound, "event not found"},
		{"not found on qr", event.ErrNotFound, http.MethodGet, eventPath + "/qr.png", "", http.StatusNotFound, "event not found"},
		{"internal on categories", errors.New("pq: password authentication failed for user sela"), http.MethodGet, "/api/v1/event-categories", "", http.StatusInternalServerError, "internal error"},
		{"internal on create", errors.New("pq: password authentication failed for user sela"), http.MethodPost, "/api/v1/events", createBody, http.StatusInternalServerError, "internal error"},
		{"internal on get", errors.New("pq: password authentication failed for user sela"), http.MethodGet, eventPath, "", http.StatusInternalServerError, "internal error"},
		{"internal on qr", errors.New("pq: password authentication failed for user sela"), http.MethodGet, eventPath + "/qr.svg", "", http.StatusInternalServerError, "internal error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			mux := newMux(stubEvents{err: tc.err}, map[string]string{"t": "host-1"})

			// Act
			rec := do(mux, tc.method, tc.path, "t", tc.body)

			// Assert
			if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Fatalf("%s %s = %d %s, want %d containing %q", tc.method, tc.path, rec.Code, rec.Body, tc.wantStatus, tc.wantMsg)
			}
			if strings.Contains(rec.Body.String(), "password") {
				t.Errorf("response leaks internal detail: %s", rec.Body)
			}
		})
	}
}
