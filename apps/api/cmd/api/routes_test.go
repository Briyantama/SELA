package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Briyantama/SELA/services/auth"
)

// stubFlow answers every sign-in call with a fixed, successful outcome.
type stubFlow struct{}

func (stubFlow) RequestOTP(context.Context, string, string) (auth.RequestResult, error) {
	return auth.RequestResult{ExpiresIn: 5 * time.Minute}, nil
}

func (stubFlow) VerifyOTP(context.Context, string, string) (auth.Session, error) {
	return auth.Session{Token: "t", HostID: "h", ExpiresIn: time.Hour}, nil
}

func (stubFlow) Authenticate(context.Context, string) (string, error) { return "h", nil }

func TestNewMux_servesHealthAndTheAuthEndpoints(t *testing.T) {
	// Arrange
	mux := newMux(auth.NewHTTPHandler(stubFlow{}, auth.HTTPConfig{}))

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"health", http.MethodGet, "/healthz", "", http.StatusOK},
		{"request otp", http.MethodPost, "/api/v1/auth/otp/request", `{"email":"host@example.test"}`, http.StatusOK},
		{"verify otp", http.MethodPost, "/api/v1/auth/otp/verify", `{"email":"host@example.test","code":"111111"}`, http.StatusOK},
		{"unknown route", http.MethodGet, "/api/v1/nothing", "", http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			// Assert
			if rec.Code != tc.want {
				t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
			}
		})
	}
}

func TestNewGRPCServer_registersTheAuthService(t *testing.T) {
	// Arrange
	srv := newGRPCServer(stubFlow{})
	defer srv.Stop()

	// Act
	info := srv.GetServiceInfo()

	// Assert
	service, ok := info["sela.auth.v1.AuthService"]
	if !ok {
		t.Fatalf("registered services = %v, want sela.auth.v1.AuthService", info)
	}
	if len(service.Methods) != 2 {
		t.Errorf("AuthService exposes %d methods, want 2", len(service.Methods))
	}
}
