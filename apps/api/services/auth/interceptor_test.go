package auth_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/Briyantama/SELA/services/auth"
)

const (
	protectedMethod = "/sela.event.v1.EventService/GetEvent"
	publicMethod    = "/sela.event.v1.EventService/ListEventCategories"
)

func withAuthorization(values ...string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.MD{"authorization": values})
}

// callInterceptor runs the interceptor and reports what the wrapped handler saw.
func callInterceptor(t *testing.T, flow auth.Flow, ctx context.Context, method string) (handlerCalled bool, hostID string, hostOK bool, err error) {
	t.Helper()
	interceptor := auth.NewUnaryHostInterceptor(flow, publicMethod)
	handler := func(ctx context.Context, req any) (any, error) {
		handlerCalled = true
		hostID, hostOK = auth.HostID(ctx)
		return "ok", nil
	}
	_, err = interceptor(ctx, "request", &grpc.UnaryServerInfo{FullMethod: method}, handler)
	return handlerCalled, hostID, hostOK, err
}

func TestUnaryHostInterceptor_letsAValidBearerTokenThroughWithTheHostID(t *testing.T) {
	tests := []struct{ name, header string }{
		{"Bearer", "Bearer token-abc"},
		{"lower case scheme", "bearer token-abc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			flow := &stubFlow{hostID: "host-42"}

			// Act
			called, hostID, ok, err := callInterceptor(t, flow, withAuthorization(tc.header), protectedMethod)

			// Assert
			if err != nil || !called {
				t.Fatalf("called=%v err=%v, want the handler to run", called, err)
			}
			if !ok || hostID != "host-42" {
				t.Errorf("HostID = (%q, %v), want (host-42, true)", hostID, ok)
			}
			if flow.gotToken != "token-abc" {
				t.Errorf("token passed to Authenticate = %q", flow.gotToken)
			}
		})
	}
}

func TestUnaryHostInterceptor_rejectsMissingOrMalformedCredentials(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{"no metadata at all", context.Background()},
		{"no authorization header", metadata.NewIncomingContext(context.Background(), metadata.MD{})},
		{"empty value", withAuthorization("")},
		{"wrong scheme", withAuthorization("Basic dXNlcjpwYXNz")},
		{"bearer without a token", withAuthorization("Bearer")},
		{"bearer with a blank token", withAuthorization("Bearer   ")},
		{"two authorization headers", withAuthorization("Bearer a", "Bearer b")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			called, _, _, err := callInterceptor(t, &stubFlow{hostID: "host-42"}, tc.ctx, protectedMethod)

			// Assert
			wantCode(t, err, codes.Unauthenticated)
			if called {
				t.Error("the handler must not run without valid credentials")
			}
		})
	}
}

func TestUnaryHostInterceptor_mapsSessionLookupOutcomes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"unknown or expired session", auth.ErrUnauthenticated, codes.Unauthenticated},
		{"store failure is generic", errors.New("redis at 10.0.0.5 refused the connection"), codes.Internal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			called, _, _, err := callInterceptor(t, &stubFlow{authErr: tc.err}, withAuthorization("Bearer token-abc"), protectedMethod)

			// Assert
			st := wantCode(t, err, tc.want)
			if called {
				t.Error("the handler must not run")
			}
			if tc.want == codes.Internal && st.Message() != "internal error" {
				t.Errorf("message = %q, want the generic message", st.Message())
			}
		})
	}
}

func TestUnaryHostInterceptor_publicMethodsSkipAuthentication(t *testing.T) {
	// Arrange
	flow := &stubFlow{authErr: errors.New("must not be consulted")}

	// Act
	called, _, hostOK, err := callInterceptor(t, flow, context.Background(), publicMethod)

	// Assert
	if err != nil || !called {
		t.Fatalf("called=%v err=%v, want the public method to run without credentials", called, err)
	}
	if hostOK {
		t.Error("a public call has no host identity")
	}
	if flow.gotToken != "" {
		t.Error("Authenticate must not run for a public method")
	}
}

func TestUnaryHostInterceptor_deniesByDefault(t *testing.T) {
	// A method nobody listed as public is protected, even if it looks harmless.
	called, _, _, err := callInterceptor(t, &stubFlow{hostID: "host-42"}, context.Background(), "/sela.event.v1.EventService/SomethingNew")

	wantCode(t, err, codes.Unauthenticated)
	if called {
		t.Error("an unlisted method must require authentication")
	}
}
