package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Briyantama/SELA/services/auth"
)

const (
	requestPath = "/api/v1/auth/otp/request"
	verifyPath  = "/api/v1/auth/otp/verify"
	cookieName  = "sela_session"
)

type apiResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *string         `json:"error"`
}

// stubFlow lets tests force specific service outcomes and observe what the adapter passed in.
type stubFlow struct {
	requestErr, verifyErr, authErr error
	session                        auth.Session
	hostID                         string

	gotClient string
	gotToken  string
}

func (f *stubFlow) RequestOTP(_ context.Context, _, client string) (auth.RequestResult, error) {
	f.gotClient = client
	return auth.RequestResult{ExpiresIn: 5 * time.Minute}, f.requestErr
}

func (f *stubFlow) VerifyOTP(_ context.Context, _, _ string) (auth.Session, error) {
	return f.session, f.verifyErr
}

func (f *stubFlow) Authenticate(_ context.Context, token string) (string, error) {
	f.gotToken = token
	return f.hostID, f.authErr
}

func newMux(flow auth.Flow, secure bool) *http.ServeMux {
	mux := http.NewServeMux()
	auth.NewHTTPHandler(flow, auth.HTTPConfig{CookieSecure: secure}).Register(mux)
	return mux
}

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeAPI(t *testing.T, rec *httptest.ResponseRecorder) apiResponse {
	t.Helper()
	var r apiResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("response is not a JSON envelope: %v (%q)", err, rec.Body.String())
	}
	return r
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	return nil
}

func TestHTTPRequestOTP_sendsTheCodeAndReportsItsLifetime(t *testing.T) {
	// Arrange
	h := newHarness(t)
	mux := newMux(h.svc, true)

	// Act
	rec := post(t, mux, requestPath, `{"email":"host@example.test"}`)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	resp := decodeAPI(t, rec)
	if !resp.Success || !strings.Contains(string(resp.Data), `"expires_in_seconds":300`) {
		t.Errorf("response = %s", rec.Body)
	}
	if h.mailer.count() != 1 {
		t.Errorf("emails sent = %d, want 1", h.mailer.count())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestHTTPVerifyOTP_setsAnHttpOnlySessionCookieAndKeepsTheTokenOutOfTheBody(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	mux := newMux(h.svc, true)
	post(t, mux, requestPath, `{"email":"host@example.test"}`)

	// Act
	rec := post(t, mux, verifyPath, `{"email":"host@example.test","code":"111111"}`)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	cookie := sessionCookie(rec)
	if cookie == nil {
		t.Fatal("no session cookie was set")
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Errorf("cookie flags = HttpOnly:%v Secure:%v SameSite:%v Path:%q", cookie.HttpOnly, cookie.Secure, cookie.SameSite, cookie.Path)
	}
	if cookie.MaxAge != int((12 * time.Hour).Seconds()) {
		t.Errorf("cookie MaxAge = %d, want 43200", cookie.MaxAge)
	}
	if len(cookie.Value) < 43 {
		t.Errorf("cookie value %q is too short to be a session token", cookie.Value)
	}
	if strings.Contains(rec.Body.String(), cookie.Value) {
		t.Error("the session token must never appear in a response body")
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"host_id"`) || !strings.Contains(body, `"is_new_host":true`) {
		t.Errorf("body = %s", body)
	}
}

func TestHTTPVerifyOTP_cookieSecureFlagFollowsConfiguration(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	mux := newMux(h.svc, false)
	post(t, mux, requestPath, `{"email":"host@example.test"}`)

	// Act
	rec := post(t, mux, verifyPath, `{"email":"host@example.test","code":"111111"}`)

	// Assert
	cookie := sessionCookie(rec)
	if cookie == nil || cookie.Secure {
		t.Fatalf("cookie = %+v, want Secure=false for local development", cookie)
	}
	if !cookie.HttpOnly {
		t.Error("HttpOnly must stay set even when Secure is off")
	}
}

func TestHTTPVerifyOTP_aWrongOrReusedCodeIsUnauthorizedAndSetsNoCookie(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	mux := newMux(h.svc, true)
	post(t, mux, requestPath, `{"email":"host@example.test"}`)
	post(t, mux, verifyPath, `{"email":"host@example.test","code":"111111"}`)

	tests := []struct{ name, code string }{
		{"reused code", "111111"},
		{"never issued", "999999"},
		{"malformed", "abc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			rec := post(t, mux, verifyPath, `{"email":"host@example.test","code":"`+tc.code+`"}`)

			// Assert
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if sessionCookie(rec) != nil {
				t.Error("a failed sign-in must not set a cookie")
			}
			if resp := decodeAPI(t, rec); resp.Success || resp.Error == nil || *resp.Error != "invalid or expired code" {
				t.Errorf("response = %s", rec.Body)
			}
		})
	}
}

func TestHTTPVerifyOTP_lockoutReturns429WithRetryAfter(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	mux := newMux(h.svc, true)
	post(t, mux, requestPath, `{"email":"host@example.test"}`)
	var rec *httptest.ResponseRecorder

	// Act
	for range 5 {
		rec = post(t, mux, verifyPath, `{"email":"host@example.test","code":"000000"}`)
	}

	// Assert
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("5th wrong attempt status = %d, want 429", rec.Code)
	}
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || secs <= 0 || secs > 15*60 {
		t.Errorf("Retry-After = %q, want 1..900 seconds", rec.Header().Get("Retry-After"))
	}
}

func TestHTTPRequestOTP_rateLimitReturns429WithRetryAfter(t *testing.T) {
	// Arrange
	h := newHarness(t)
	mux := newMux(h.svc, true)
	for range 3 {
		post(t, mux, requestPath, `{"email":"host@example.test"}`)
	}

	// Act
	rec := post(t, mux, requestPath, `{"email":"host@example.test"}`)

	// Assert
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if secs, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || secs <= 0 {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

func TestHTTPRequests_rejectBadInput(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		body        string
		contentType string
		want        int
	}{
		{"invalid email", requestPath, `{"email":"nope"}`, "application/json", http.StatusBadRequest},
		{"malformed json", requestPath, `{"email":`, "application/json", http.StatusBadRequest},
		{"empty body", requestPath, ``, "application/json", http.StatusBadRequest},
		{"unknown field", requestPath, `{"email":"host@example.test","admin":true}`, "application/json", http.StatusBadRequest},
		{"trailing data", requestPath, `{"email":"host@example.test"}{"x":1}`, "application/json", http.StatusBadRequest},
		{"oversized body", requestPath, `{"email":"` + strings.Repeat("a", 8192) + `"}`, "application/json", http.StatusRequestEntityTooLarge},
		{"form encoded", requestPath, `email=host@example.test`, "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"no content type", requestPath, `{"email":"host@example.test"}`, "", http.StatusUnsupportedMediaType},
		{"verify invalid email", verifyPath, `{"email":"nope","code":"111111"}`, "application/json", http.StatusBadRequest},
		{"verify unknown field", verifyPath, `{"email":"host@example.test","code":"111111","x":1}`, "application/json", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h := newHarness(t)
			mux := newMux(h.svc, true)
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			rec := httptest.NewRecorder()

			// Act
			mux.ServeHTTP(rec, req)

			// Assert
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
			if h.mailer.count() != 0 {
				t.Error("no email may be sent for a rejected request")
			}
		})
	}
}

func TestHTTPEndpoints_onlyAcceptPost(t *testing.T) {
	// Arrange
	h := newHarness(t)
	mux := newMux(h.svc, true)

	for _, path := range []string{requestPath, verifyPath} {
		// Act
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// Assert
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s status = %d, want 405", path, rec.Code)
		}
	}
}

func TestHTTPRequestOTP_deliveryFailureIsA502WithoutLeakingDetails(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.mailer.err = errors.New("smtp password rejected for mailer@internal")
	mux := newMux(h.svc, true)

	// Act
	rec := post(t, mux, requestPath, `{"email":"host@example.test"}`)

	// Assert
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "smtp") || strings.Contains(rec.Body.String(), "internal") {
		t.Errorf("response leaks delivery details: %s", rec.Body)
	}
}

func TestHTTPInternalErrors_areGeneric(t *testing.T) {
	// Arrange
	flow := &stubFlow{
		requestErr: errors.New("redis at 10.0.0.5 refused the connection"),
		verifyErr:  errors.New("pq: password authentication failed for user sela"),
	}
	mux := newMux(flow, true)
	bodies := map[string]string{
		requestPath: `{"email":"host@example.test"}`,
		verifyPath:  `{"email":"host@example.test","code":"111111"}`,
	}

	for path, body := range bodies {
		// Act
		rec := post(t, mux, path, body)

		// Assert
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s status = %d, want 500", path, rec.Code)
		}
		if resp := decodeAPI(t, rec); resp.Error == nil || *resp.Error != "internal error" {
			t.Errorf("%s response = %s, want the generic message", path, rec.Body)
		}
	}
}

func TestHTTPRequestOTP_usesTheConnectionAddressAndIgnoresForwardedHeaders(t *testing.T) {
	// Arrange
	flow := &stubFlow{}
	mux := newMux(flow, true)
	req := httptest.NewRequest(http.MethodPost, requestPath, strings.NewReader(`{"email":"host@example.test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.RemoteAddr = "192.0.2.44:51000"

	// Act
	mux.ServeHTTP(httptest.NewRecorder(), req)

	// Assert
	if flow.gotClient != "192.0.2.44" {
		t.Fatalf("client key = %q, want the connection address 192.0.2.44 (a spoofable header must not be trusted)", flow.gotClient)
	}
}

func TestRequireHost_letsAnAuthenticatedRequestThroughWithTheHostID(t *testing.T) {
	// Arrange
	flow := &stubFlow{hostID: "host-42"}
	handler := auth.NewHTTPHandler(flow, auth.HTTPConfig{}).RequireHost(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.HostID(r.Context())
		if !ok || id != "host-42" {
			t.Errorf("HostID = (%q, %v), want (host-42, true)", id, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "token-abc"})
	rec := httptest.NewRecorder()

	// Act
	handler.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if flow.gotToken != "token-abc" {
		t.Errorf("token passed to Authenticate = %q", flow.gotToken)
	}
}

func TestRequireHost_rejectsMissingAndUnknownSessions(t *testing.T) {
	tests := []struct {
		name   string
		cookie *http.Cookie
		flow   *stubFlow
		want   int
	}{
		{"no cookie", nil, &stubFlow{}, http.StatusUnauthorized},
		{"unknown session", &http.Cookie{Name: cookieName, Value: "x"}, &stubFlow{authErr: auth.ErrUnauthenticated}, http.StatusUnauthorized},
		{"store failure", &http.Cookie{Name: cookieName, Value: "x"}, &stubFlow{authErr: errors.New("redis down")}, http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			called := false
			handler := auth.NewHTTPHandler(tc.flow, auth.HTTPConfig{}).RequireHost(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			rec := httptest.NewRecorder()

			// Act
			handler.ServeHTTP(rec, req)

			// Assert
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if called {
				t.Error("the protected handler must not run without a valid session")
			}
		})
	}
}

func TestHostID_isAbsentOutsideAuthenticatedRequests(t *testing.T) {
	if id, ok := auth.HostID(context.Background()); ok || id != "" {
		t.Fatalf("HostID on a bare context = (%q, %v), want empty", id, ok)
	}
}
