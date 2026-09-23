package media_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Briyantama/SELA/internal/objstore"
	"github.com/Briyantama/SELA/services/auth"
	"github.com/Briyantama/SELA/services/media"
)

const (
	eventA   = "11111111-1111-4111-8111-111111111111"
	mediaA   = "22222222-2222-4222-8222-222222222222"
	goodTok  = "guest-token"
	hostTok  = "host-token"
	hostID   = "33333333-3333-4333-8333-333333333333"
	cookieNm = "sela_guest"
)

// stubGuests records calls and returns canned results, so the adapter is tested without storage.
type stubGuests struct {
	err       error
	started   media.StartedSession
	upload    media.Upload
	item      media.Item
	items     []media.Item
	gotToken  string
	gotNick   *string
	gotType   string
	gotSize   int64
	gotHostID string
}

func (s *stubGuests) StartSession(_ context.Context, _ string, token string, nickname *string) (media.StartedSession, error) {
	s.gotToken, s.gotNick = token, nickname
	return s.started, s.err
}

func (s *stubGuests) Authenticate(_ context.Context, eventID, token string) (media.Guest, error) {
	if token != goodTok {
		return media.Guest{}, media.ErrUnauthorized
	}
	return media.Guest{EventID: eventID, SessionID: "s-1"}, nil
}

func (s *stubGuests) BeginUpload(_ context.Context, _ media.Guest, contentType string, size int64) (media.Upload, error) {
	s.gotType, s.gotSize = contentType, size
	return s.upload, s.err
}

func (s *stubGuests) CompleteUpload(context.Context, media.Guest, string) (media.Item, error) {
	return s.item, s.err
}

func (s *stubGuests) MyMedia(context.Context, media.Guest) ([]media.Item, error)    { return s.items, s.err }
func (s *stubGuests) HallOfFame(context.Context, media.Guest) ([]media.Item, error) { return s.items, s.err }
func (s *stubGuests) HostMedia(_ context.Context, host, _ string) ([]media.Item, error) {
	s.gotHostID = host
	return s.items, s.err
}

type fakeHostAuth struct{}

func (fakeHostAuth) RequestOTP(context.Context, string, string) (auth.RequestResult, error) {
	return auth.RequestResult{}, nil
}
func (fakeHostAuth) VerifyOTP(context.Context, string, string) (auth.Session, error) {
	return auth.Session{}, nil
}
func (fakeHostAuth) Authenticate(_ context.Context, token string) (string, error) {
	if token == hostTok {
		return hostID, nil
	}
	return "", auth.ErrUnauthenticated
}

func newMux(svc media.Guests, secure bool) *http.ServeMux {
	mux := http.NewServeMux()
	guard := auth.NewHTTPHandler(fakeHostAuth{}, auth.HTTPConfig{})
	media.NewHTTPHandler(svc, guard, media.HTTPConfig{CookieSecure: secure}).Register(mux)
	return mux
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *string         `json:"error"`
}

func send(mux http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeInto[T any](t *testing.T, rec *httptest.ResponseRecorder) (T, envelope) {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", rec.Body.String(), err)
	}
	var data T
	if len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("decode data %s: %v", env.Data, err)
		}
	}
	return data, env
}

var guestCookie = &http.Cookie{Name: cookieNm, Value: goodTok}

func TestHTTPGuestSession_setsAScopedHttpOnlyCookieAndNeverReturnsTheToken(t *testing.T) {
	// Arrange
	five := 5
	svc := &stubGuests{started: media.StartedSession{
		Token: "secret-token-value", Guest: media.Guest{EventID: eventA, SessionID: "s-1"},
		ShotLimit: &five, ShotsRemaining: &five, InvolvesMinors: true, ExpiresIn: 72 * time.Hour,
	}}

	// Act
	rec := send(newMux(svc, true), "POST", "/api/v1/events/"+eventA+"/guest-session", `{"nickname":"Budi"}`)

	// Assert
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secret-token-value") {
		t.Fatal("the guest token leaked into the response body")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want one", cookies)
	}
	c := cookies[0]
	if c.Name != cookieNm || c.Value != "secret-token-value" || !c.HttpOnly || !c.Secure ||
		c.SameSite != http.SameSiteLaxMode || c.Path != "/api/v1/events/"+eventA || c.MaxAge != int((72*time.Hour).Seconds()) {
		t.Fatalf("cookie = %+v", c)
	}
	data, _ := decodeInto[map[string]any](t, rec)
	if data["session_id"] != "s-1" || data["shots_remaining"] != float64(5) || data["involves_minors"] != true {
		t.Fatalf("data = %v", data)
	}
	if svc.gotNick == nil || *svc.gotNick != "Budi" {
		t.Fatalf("nickname passed = %v", svc.gotNick)
	}
}

func TestHTTPGuestSession_resumesWithTheExistingCookieAndAcceptsAnEmptyBody(t *testing.T) {
	// Arrange
	svc := &stubGuests{started: media.StartedSession{Token: goodTok, Resumed: true, Guest: media.Guest{EventID: eventA, SessionID: "s-1"}, ExpiresIn: time.Hour}}

	// Act
	rec := send(newMux(svc, false), "POST", "/api/v1/events/"+eventA+"/guest-session", "", guestCookie)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a resumed session: %s", rec.Code, rec.Body)
	}
	if svc.gotToken != goodTok || svc.gotNick != nil {
		t.Fatalf("token %q, nickname %v passed", svc.gotToken, svc.gotNick)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Secure {
		t.Fatalf("cookies = %v, want one non-Secure cookie when CookieSecure is off", c)
	}
}

func TestHTTPGuestRoutes_alwaysSendNoIndexAndNoStore(t *testing.T) {
	svc := &stubGuests{err: media.ErrNotFound}
	mux := newMux(svc, true)

	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/events/" + eventA + "/guest-session"},
		{"POST", "/api/v1/events/" + eventA + "/media/uploads"},
		{"POST", "/api/v1/events/" + eventA + "/media/" + mediaA + "/complete"},
		{"GET", "/api/v1/events/" + eventA + "/my-media"},
		{"GET", "/api/v1/events/" + eventA + "/hall-of-fame"},
		{"GET", "/api/v1/events/" + eventA + "/media"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			// Act
			rec := send(mux, tc.method, tc.path, "")

			// Assert
			h := rec.Header()
			if h.Get("X-Robots-Tag") != "noindex, nofollow" || h.Get("Cache-Control") != "no-store" ||
				h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("headers = %v", h)
			}
		})
	}
}

func TestHTTPGuestRoutes_requireTheGuestCookie(t *testing.T) {
	mux := newMux(&stubGuests{}, true)

	for _, path := range []string{"/media/uploads", "/media/" + mediaA + "/complete", "/my-media", "/hall-of-fame"} {
		t.Run(path, func(t *testing.T) {
			method := "GET"
			if strings.HasPrefix(path, "/media/") {
				method = "POST"
			}

			// Act
			missing := send(mux, method, "/api/v1/events/"+eventA+path, `{}`)
			wrong := send(mux, method, "/api/v1/events/"+eventA+path, `{}`, &http.Cookie{Name: cookieNm, Value: "forged"})

			// Assert
			if missing.Code != http.StatusUnauthorized || wrong.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d / %d, want 401", missing.Code, wrong.Code)
			}
		})
	}
}

func TestHTTPUploads_returnsTheSignedRequestAndShotsLeft(t *testing.T) {
	// Arrange
	two := 2
	expires := time.Date(2026, 12, 1, 10, 10, 0, 0, time.UTC)
	svc := &stubGuests{upload: media.Upload{
		MediaID:        mediaA,
		Request:        objstore.PresignedRequest{Method: "PUT", URL: "https://bucket/x", Headers: map[string]string{"Content-Type": "image/jpeg"}, ExpiresAt: expires},
		ShotsRemaining: &two,
	}}

	// Act
	rec := send(newMux(svc, true), "POST", "/api/v1/events/"+eventA+"/media/uploads",
		`{"content_type":"image/jpeg","size_bytes":1234}`, guestCookie)

	// Assert
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	data, _ := decodeInto[struct {
		MediaID string `json:"media_id"`
		Upload  struct {
			Method    string            `json:"method"`
			URL       string            `json:"url"`
			Headers   map[string]string `json:"headers"`
			ExpiresAt time.Time         `json:"expires_at"`
		} `json:"upload"`
		ShotsRemaining *int `json:"shots_remaining"`
	}](t, rec)
	if data.MediaID != mediaA || data.Upload.Method != "PUT" || data.Upload.URL != "https://bucket/x" ||
		data.Upload.Headers["Content-Type"] != "image/jpeg" || !data.Upload.ExpiresAt.Equal(expires) ||
		data.ShotsRemaining == nil || *data.ShotsRemaining != 2 {
		t.Fatalf("data = %+v", data)
	}
	if svc.gotType != "image/jpeg" || svc.gotSize != 1234 {
		t.Fatalf("passed %q / %d", svc.gotType, svc.gotSize)
	}
}

func TestHTTPUploads_rejectsMalformedBodies(t *testing.T) {
	mux := newMux(&stubGuests{}, true)

	for name, body := range map[string]string{
		"unknown field": `{"content_type":"image/jpeg","size_bytes":1,"path":"../x"}`,
		"not json":      `content_type=image/jpeg`,
		"wrong type":    `{"content_type":"image/jpeg","size_bytes":"big"}`,
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			rec := send(mux, "POST", "/api/v1/events/"+eventA+"/media/uploads", body, guestCookie)

			// Assert
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestHTTPErrors_mapToStableStatusesWithoutLeakingDetails(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"not found", media.ErrNotFound, http.StatusNotFound},
		{"unsupported", media.ErrUnsupportedType, http.StatusUnsupportedMediaType},
		{"too large", media.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{"camera full", media.ErrQuotaExhausted, http.StatusTooManyRequests},
		{"validation", &media.ValidationError{Field: "size_bytes", Message: "must be positive"}, http.StatusBadRequest},
		{"upload missing", media.ErrUploadMissing, http.StatusConflict},
		{"rejected", errors.Join(media.ErrRejected, errors.New("jpeg: secret parser detail")), http.StatusUnprocessableEntity},
		{"internal", errors.New("pq: connection refused at 10.0.0.5"), http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			mux := newMux(&stubGuests{err: tc.err}, true)

			// Act
			rec := send(mux, "POST", "/api/v1/events/"+eventA+"/media/"+mediaA+"/complete", "", guestCookie)

			// Assert
			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d", rec.Code, tc.code)
			}
			_, env := decodeInto[any](t, rec)
			if env.Success || env.Error == nil {
				t.Fatalf("envelope = %+v", env)
			}
			for _, leak := range []string{"secret parser", "10.0.0.5"} {
				if strings.Contains(*env.Error, leak) {
					t.Fatalf("error %q leaks %q", *env.Error, leak)
				}
			}
		})
	}
}

func TestHTTPGalleries_listItemsWithoutStorageKeysOrSessionIDs(t *testing.T) {
	// Arrange
	size := int64(99)
	ready := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	session := "s-secret"
	item := media.Item{
		Media: media.Media{ID: mediaA, EventID: eventA, SessionID: &session, Kind: media.KindPhoto, ContentType: "image/jpeg",
			ObjectKey: "media/secret-key", SizeBytes: &size, Processing: media.ProcessingReady, Status: media.StatusActive,
			UploadedAt: ready, ReadyAt: &ready},
		URL: "https://bucket/signed",
	}
	svc := &stubGuests{items: []media.Item{item}}
	mux := newMux(svc, true)

	for _, tc := range []struct {
		path   string
		cookie *http.Cookie
	}{
		{"/my-media", guestCookie},
		{"/hall-of-fame", guestCookie},
		{"/media", &http.Cookie{Name: "sela_session", Value: hostTok}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			// Act
			rec := send(mux, "GET", "/api/v1/events/"+eventA+tc.path, "", tc.cookie)

			// Assert
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			if bytes.Contains(rec.Body.Bytes(), []byte("secret-key")) || bytes.Contains(rec.Body.Bytes(), []byte("s-secret")) {
				t.Fatalf("response leaks the object key or session id: %s", rec.Body)
			}
			data, _ := decodeInto[struct {
				Items []map[string]any `json:"items"`
			}](t, rec)
			if len(data.Items) != 1 || data.Items[0]["media_id"] != mediaA || data.Items[0]["url"] != "https://bucket/signed" ||
				data.Items[0]["processing_state"] != "ready" {
				t.Fatalf("items = %v", data.Items)
			}
		})
	}
	if svc.gotHostID != hostID {
		t.Fatalf("host id passed = %q", svc.gotHostID)
	}
}

func TestHTTPHostMedia_requiresAHostSession(t *testing.T) {
	// Act
	rec := send(newMux(&stubGuests{}, true), "GET", "/api/v1/events/"+eventA+"/media", "", guestCookie)

	// Assert
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a host session", rec.Code)
	}
}

func TestHTTP_fullGuestFlowAgainstTheRealService(t *testing.T) {
	// Arrange
	f := newFixture(t)
	eventID := f.event(t, eventOpts{shotLimit: limit(2)})
	mux := newMux(f.svc, false)
	base := "/api/v1/events/" + eventID

	// Act: join, ask for an upload URL, PUT the bytes, complete, list.
	joined := send(mux, "POST", base+"/guest-session", "")
	cookie := joined.Result().Cookies()[0]
	body := jpegWithGPS(t)
	began := send(mux, "POST", base+"/media/uploads", `{"content_type":"image/jpeg","size_bytes":`+strconv.Itoa(len(body))+`}`, cookie)
	up, _ := decodeInto[struct {
		MediaID string `json:"media_id"`
		Upload  struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"upload"`
	}](t, began)
	req := objstore.PresignedRequest{URL: up.Upload.URL, Headers: up.Upload.Headers}
	if err := f.store.Upload(req, "image/jpeg", body); err != nil {
		t.Fatalf("browser PUT: %v", err)
	}
	done := send(mux, "POST", base+"/media/"+up.MediaID+"/complete", "", cookie)
	mine := send(mux, "GET", base+"/my-media", "", cookie)

	// Assert
	if joined.Code != http.StatusCreated || began.Code != http.StatusCreated || done.Code != http.StatusOK || mine.Code != http.StatusOK {
		t.Fatalf("statuses = %d %d %d %d: %s", joined.Code, began.Code, done.Code, mine.Code, done.Body)
	}
	list, _ := decodeInto[struct {
		Items []map[string]any `json:"items"`
	}](t, mine)
	if len(list.Items) != 1 || list.Items[0]["media_id"] != up.MediaID || list.Items[0]["processing_state"] != "ready" {
		t.Fatalf("my-media = %v", list.Items)
	}
}
