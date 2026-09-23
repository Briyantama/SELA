package rbac_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Briyantama/SELA/services/auth"
	"github.com/Briyantama/SELA/services/rbac"
)

// fakeAuth resolves session tokens to host ids, standing in for the real sign-in flow.
type fakeAuth struct{ tokens map[string]string }

func (fakeAuth) RequestOTP(context.Context, string, string) (auth.RequestResult, error) {
	return auth.RequestResult{}, nil
}
func (fakeAuth) VerifyOTP(context.Context, string, string) (auth.Session, error) {
	return auth.Session{}, nil
}
func (a fakeAuth) Authenticate(_ context.Context, token string) (string, error) {
	if id, ok := a.tokens[token]; ok {
		return id, nil
	}
	return "", auth.ErrUnauthenticated
}

// stubProfileService forces specific use-case outcomes and records the last language override.
type stubProfileService struct {
	profile   rbac.Profile
	err       error
	allow     bool
	lastLang  string
	lastInput rbac.PreferencesInput
}

func (s *stubProfileService) Profile(_ context.Context, _ string, lang string) (rbac.Profile, error) {
	s.lastLang = lang
	return s.profile, s.err
}

func (s *stubProfileService) UpdatePreferences(_ context.Context, _ string, in rbac.PreferencesInput) (rbac.Profile, error) {
	s.lastInput = in
	return s.profile, s.err
}

func (s *stubProfileService) Has(context.Context, string, rbac.Permission) (bool, error) {
	return s.allow, nil
}

func newMux(svc rbac.ProfileService, tokens map[string]string) *http.ServeMux {
	guard := auth.NewHTTPHandler(fakeAuth{tokens: tokens}, auth.HTTPConfig{})
	mux := http.NewServeMux()
	rbac.NewHTTPHandler(svc, guard).Register(mux)
	return mux
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

type httpEnvelope[T any] struct {
	Success bool    `json:"success"`
	Data    T       `json:"data"`
	Error   *string `json:"error"`
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) httpEnvelope[T] {
	t.Helper()
	var env httpEnvelope[T]
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("not a JSON envelope: %v (%q)", err, rec.Body.String())
	}
	return env
}

func samplePreference() rbac.Profile {
	priority := 1
	return rbac.Profile{
		RoleCode: "host", RoleName: "Host",
		Preferences: rbac.Preferences{
			Theme: "light", ThemeSource: "default", Language: "id", LanguageSource: "default",
			SupportedLanguages: []rbac.Language{{Code: "id", Name: "Bahasa Indonesia"}, {Code: "en", Name: "English"}},
		},
		Menus: []rbac.Menu{{
			Code: "events_new", Type: "link", Label: "Buat Acara Baru", SortOrder: 10,
			Layout: rbac.MenuLayout{MobilePriority: &priority},
		}},
		Permissions: []string{"events:create", "profile:update"},
		Actions: map[string][]rbac.ActionPermission{
			"events": {{Code: "create", Permission: "events:create", Label: "Buat acara", Variant: "primary",
				Placement: rbac.Placement{Desktop: "toolbar", Tablet: "toolbar_icon", Mobile: "fab"}}},
		},
		Dropdowns: map[string]rbac.Dropdown{},
	}
}

func TestGetMe_requiresASession(t *testing.T) {
	// Arrange
	mux := newMux(&stubProfileService{}, nil)

	// Act
	rec := do(mux, http.MethodGet, "/api/v1/auth/me", "", "")

	// Assert
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestGetMe_returnsTheProfileForTheAuthenticatedHost(t *testing.T) {
	// Arrange
	svc := &stubProfileService{profile: samplePreference()}
	mux := newMux(svc, map[string]string{"tok": "host-1"})

	// Act
	rec := do(mux, http.MethodGet, "/api/v1/auth/me?lang=en", "tok", "")

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if svc.lastLang != "en" {
		t.Errorf("lang passed to Profile = %q, want en", svc.lastLang)
	}
	body := rec.Body.String()
	for _, want := range []string{`"role_code":"host"`, `"theme":"light"`, `"events_new"`, `"events:create"`, `"fab"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

func TestGetMe_mapsAServiceErrorToAGenericInternalError(t *testing.T) {
	// Arrange
	svc := &stubProfileService{err: rbac.ErrUnknownHost}
	mux := newMux(svc, map[string]string{"tok": "host-1"})

	// Act
	rec := do(mux, http.MethodGet, "/api/v1/auth/me", "tok", "")

	// Assert
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	env := decode[any](t, rec)
	if env.Error == nil || *env.Error != "internal error" || strings.Contains(*env.Error, "unknown host") {
		t.Errorf("error = %v, want a generic message", env.Error)
	}
}

func TestPatchPreferences_requiresASessionAndThePermission(t *testing.T) {
	// Arrange
	svc := &stubProfileService{profile: samplePreference(), allow: false}
	mux := newMux(svc, map[string]string{"tok": "host-1"})

	// Act + Assert: no session.
	if rec := do(mux, http.MethodPatch, "/api/v1/me/preferences", "", `{"theme":"dark"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session status = %d, want 401", rec.Code)
	}

	// Act + Assert: authenticated but the permission is not granted.
	rec := do(mux, http.MethodPatch, "/api/v1/me/preferences", "tok", `{"theme":"dark"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestPatchPreferences_updatesWhenPermitted(t *testing.T) {
	// Arrange
	svc := &stubProfileService{profile: samplePreference(), allow: true}
	mux := newMux(svc, map[string]string{"tok": "host-1"})

	// Act
	rec := do(mux, http.MethodPatch, "/api/v1/me/preferences", "tok", `{"theme":"dark","language":"en"}`)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if svc.lastInput.Theme == nil || *svc.lastInput.Theme != "dark" {
		t.Errorf("theme sent = %v, want dark", svc.lastInput.Theme)
	}
	if svc.lastInput.Language == nil || *svc.lastInput.Language != "en" {
		t.Errorf("language sent = %v, want en", svc.lastInput.Language)
	}
}

func TestPatchPreferences_rejectsMalformedJSON(t *testing.T) {
	// Arrange
	svc := &stubProfileService{profile: samplePreference(), allow: true}
	mux := newMux(svc, map[string]string{"tok": "host-1"})

	// Act
	rec := do(mux, http.MethodPatch, "/api/v1/me/preferences", "tok", `{not json`)

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPatchPreferences_mapsAValidationErrorTo400(t *testing.T) {
	// Arrange
	svc := &stubProfileService{allow: true, err: &rbac.ValidationError{Message: "theme must be light or dark"}}
	mux := newMux(svc, map[string]string{"tok": "host-1"})

	// Act
	rec := do(mux, http.MethodPatch, "/api/v1/me/preferences", "tok", `{"theme":"purple"}`)

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	env := decode[any](t, rec)
	if env.Error == nil || *env.Error != "theme must be light or dark" {
		t.Errorf("error = %v", env.Error)
	}
}

func TestRoutes_rejectUnsupportedMethods(t *testing.T) {
	// Arrange
	mux := newMux(&stubProfileService{allow: true}, map[string]string{"tok": "host-1"})

	// Act + Assert
	if rec := do(mux, http.MethodPost, "/api/v1/auth/me", "tok", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /auth/me status = %d, want 405", rec.Code)
	}
	if rec := do(mux, http.MethodGet, "/api/v1/me/preferences", "tok", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /me/preferences status = %d, want 405", rec.Code)
	}
}
