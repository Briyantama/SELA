package media_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Briyantama/SELA/services/auth"
	"github.com/Briyantama/SELA/services/media"
)

type limitCall struct{ scope, subject string }

// stubLimiter blocks the scopes listed in block and records every call.
type stubLimiter struct {
	block map[string]bool
	err   error
	calls []limitCall
}

func (s *stubLimiter) Allow(_ context.Context, scope, subject string, _ int, _ time.Duration) (bool, time.Duration, error) {
	s.calls = append(s.calls, limitCall{scope, subject})
	if s.err != nil {
		return false, 0, s.err
	}
	if s.block[scope] {
		return false, 30 * time.Second, nil
	}
	return true, 0, nil
}

func newLimitedMux(svc media.Guests, l media.Limiter) *http.ServeMux {
	mux := http.NewServeMux()
	guard := auth.NewHTTPHandler(fakeHostAuth{}, auth.HTTPConfig{})
	media.NewHTTPHandler(svc, guard, media.HTTPConfig{Limiter: l}).Register(mux)
	return mux
}

const (
	sessionPath = "/api/v1/events/" + eventA + "/guest-session"
	uploadPath  = "/api/v1/events/" + eventA + "/media/uploads"
	uploadBody  = `{"content_type":"image/jpeg","size_bytes":1024}`
)

func TestHTTPRateLimit_guestSessionIsBlockedPerClientBeforeTheServiceRuns(t *testing.T) {
	// Arrange
	svc := &stubGuests{}
	lim := &stubLimiter{block: map[string]bool{"guest-session": true}}

	// Act
	rec := send(newLimitedMux(svc, lim), "POST", sessionPath, "")

	// Assert
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("a blocked request must not set a guest cookie")
	}
	if svc.gotToken != "" || svc.gotNick != nil {
		t.Error("StartSession ran although the client was rate limited")
	}
	_, env := decodeInto[map[string]any](t, rec)
	if env.Success || env.Error == nil || *env.Error == "" {
		t.Errorf("envelope = %+v, want a fixed error message", env)
	}
}

func TestHTTPRateLimit_uploadsAreBlockedPerSessionAndPerClient(t *testing.T) {
	for _, scope := range []string{"upload", "upload-ip"} {
		t.Run(scope, func(t *testing.T) {
			// Arrange
			svc := &stubGuests{}
			lim := &stubLimiter{block: map[string]bool{scope: true}}

			// Act
			rec := send(newLimitedMux(svc, lim), "POST", uploadPath, uploadBody, guestCookie)

			// Assert
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429: %s", rec.Code, rec.Body)
			}
			if rec.Header().Get("Retry-After") != "30" {
				t.Errorf("Retry-After = %q, want 30", rec.Header().Get("Retry-After"))
			}
			if svc.gotType != "" {
				t.Error("BeginUpload ran although the guest was rate limited")
			}
		})
	}
}

func TestHTTPRateLimit_keysOnTheConnectionAddressNotForwardedHeaders(t *testing.T) {
	// Arrange
	lim := &stubLimiter{}
	req := httptest.NewRequest("POST", sessionPath, strings.NewReader(""))
	req.RemoteAddr = "192.0.2.1:4242"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	req.Header.Set("X-Real-IP", "198.51.100.9")

	// Act
	newLimitedMux(&stubGuests{}, lim).ServeHTTP(httptest.NewRecorder(), req)

	// Assert
	if len(lim.calls) != 1 || lim.calls[0] != (limitCall{"guest-session", "192.0.2.1"}) {
		t.Fatalf("calls = %+v, want one guest-session call for 192.0.2.1", lim.calls)
	}
}

func TestHTTPRateLimit_uploadCountsTheGuestSessionAndTheClient(t *testing.T) {
	// Arrange
	lim := &stubLimiter{}
	req := httptest.NewRequest("POST", uploadPath, strings.NewReader(uploadBody))
	req.RemoteAddr = "192.0.2.1:4242"
	req.AddCookie(guestCookie)

	// Act
	newLimitedMux(&stubGuests{}, lim).ServeHTTP(httptest.NewRecorder(), req)

	// Assert
	want := []limitCall{{"upload", "s-1"}, {"upload-ip", "192.0.2.1"}}
	if len(lim.calls) != 2 || lim.calls[0] != want[0] || lim.calls[1] != want[1] {
		t.Fatalf("calls = %+v, want %+v", lim.calls, want)
	}
}

func TestHTTPRateLimit_limiterFailureIsAGenericServerError(t *testing.T) {
	// Arrange
	svc := &stubGuests{}
	lim := &stubLimiter{err: errors.New("redis: connection refused")}

	// Act
	rec := send(newLimitedMux(svc, lim), "POST", sessionPath, "")

	// Assert
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "redis") {
		t.Errorf("limiter error leaked: %s", rec.Body)
	}
}

func TestHTTPRateLimit_onlyTheAbuseProneRoutesAreLimited(t *testing.T) {
	// Arrange
	lim := &stubLimiter{block: map[string]bool{"guest-session": true, "upload": true, "upload-ip": true}}
	mux := newLimitedMux(&stubGuests{}, lim)

	// Act
	complete := send(mux, "POST", "/api/v1/events/"+eventA+"/media/"+mediaA+"/complete", "", guestCookie)
	mine := send(mux, "GET", "/api/v1/events/"+eventA+"/my-media", "", guestCookie)

	// Assert
	if complete.Code == http.StatusTooManyRequests || mine.Code == http.StatusTooManyRequests {
		t.Fatalf("complete=%d my-media=%d, want neither rate limited", complete.Code, mine.Code)
	}
	if len(lim.calls) != 0 {
		t.Errorf("limiter consulted for unlimited routes: %+v", lim.calls)
	}
}
