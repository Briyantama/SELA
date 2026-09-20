package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	eventv1 "github.com/Briyantama/SELA/gen/go/event/v1"
	"github.com/Briyantama/SELA/services/auth"
	"github.com/Briyantama/SELA/services/event"
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

// stubEvents answers event calls with fixed, successful outcomes.
type stubEvents struct{}

func (stubEvents) ListCategories(context.Context) ([]event.Category, error) {
	return []event.Category{{Code: "lainnya", Name: "Lainnya", ThemeKey: "neutral", ShotLimitDefault: "tbd", RevealDefault: "tbd", EffectiveRevealMode: "instant"}}, nil
}

func (stubEvents) CreateEvent(context.Context, string, event.CreateInput) (event.Event, error) {
	return event.Event{ID: "e", CreatedAt: time.Now()}, nil
}

func (stubEvents) GetEvent(context.Context, string, string) (event.Event, error) {
	return event.Event{ID: "e", CreatedAt: time.Now()}, nil
}

func (stubEvents) QRCode(context.Context, string, string, event.QRFormat) ([]byte, string, error) {
	return []byte("png"), "image/png", nil
}

func (stubEvents) ResolveShortCode(context.Context, string) (event.PublicEvent, error) {
	return event.PublicEvent{EventID: "e"}, nil
}

func TestNewMux_servesHealthAuthAndEventEndpoints(t *testing.T) {
	// Arrange
	authHTTP := auth.NewHTTPHandler(stubFlow{}, auth.HTTPConfig{})
	mux := newMux(authHTTP, event.NewHTTPHandler(stubEvents{}, authHTTP))

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		cookie bool
		want   int
	}{
		{"health", http.MethodGet, "/healthz", "", false, http.StatusOK},
		{"request otp", http.MethodPost, "/api/v1/auth/otp/request", `{"email":"host@example.test"}`, false, http.StatusOK},
		{"verify otp", http.MethodPost, "/api/v1/auth/otp/verify", `{"email":"host@example.test","code":"111111"}`, false, http.StatusOK},
		{"categories are public", http.MethodGet, "/api/v1/event-categories", "", false, http.StatusOK},
		{"create event needs a session", http.MethodPost, "/api/v1/events", `{"category_code":"lainnya","name":"A","event_date":"2026-12-05"}`, false, http.StatusUnauthorized},
		{"create event with a session", http.MethodPost, "/api/v1/events", `{"category_code":"lainnya","name":"A","event_date":"2026-12-05"}`, true, http.StatusCreated},
		{"get event needs a session", http.MethodGet, "/api/v1/events/00000000-0000-4000-8000-000000000000", "", false, http.StatusUnauthorized},
		{"get event with a session", http.MethodGet, "/api/v1/events/00000000-0000-4000-8000-000000000000", "", true, http.StatusOK},
		{"qr needs a session", http.MethodGet, "/api/v1/events/00000000-0000-4000-8000-000000000000/qr.png", "", false, http.StatusUnauthorized},
		{"qr with a session", http.MethodGet, "/api/v1/events/00000000-0000-4000-8000-000000000000/qr.png", "", true, http.StatusOK},
		{"unknown route", http.MethodGet, "/api/v1/nothing", "", false, http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.cookie {
				req.AddCookie(&http.Cookie{Name: "sela_session", Value: "any"})
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			// Assert
			if rec.Code != tc.want {
				t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
			}
		})
	}
}

func TestNewGRPCServer_registersBothServices(t *testing.T) {
	// Arrange
	srv := newGRPCServer(stubFlow{}, stubEvents{})
	defer srv.Stop()

	// Act
	info := srv.GetServiceInfo()

	// Assert
	if svc, ok := info["sela.auth.v1.AuthService"]; !ok || len(svc.Methods) != 2 {
		t.Errorf("AuthService = %v (registered: %v)", svc, info)
	}
	if svc, ok := info["sela.event.v1.EventService"]; !ok || len(svc.Methods) != 3 {
		t.Errorf("EventService = %v (registered: %v)", svc, info)
	}
}

func TestNewGRPCServer_protectsEventCallsButLeavesCategoriesAndSignInPublic(t *testing.T) {
	// Arrange
	lis := bufconn.Listen(1 << 20)
	srv := newGRPCServer(stubFlow{}, stubEvents{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	client := eventv1.NewEventServiceClient(conn)

	// Act
	cats, catsErr := client.ListEventCategories(context.Background(), &eventv1.ListEventCategoriesRequest{})
	_, createErr := client.CreateEvent(context.Background(), &eventv1.CreateEventRequest{CategoryCode: "lainnya", Name: "A", EventDate: "2026-12-05"})
	_, getErr := client.GetEvent(context.Background(), &eventv1.GetEventRequest{EventId: "e"})

	// Assert
	if catsErr != nil || len(cats.GetCategories()) != 1 {
		t.Errorf("public categories = %v, %v", cats, catsErr)
	}
	for name, e := range map[string]error{"create": createErr, "get": getErr} {
		if st, _ := status.FromError(e); st.Code() != codes.Unauthenticated {
			t.Errorf("%s without a token = %v, want Unauthenticated", name, e)
		}
	}
}
