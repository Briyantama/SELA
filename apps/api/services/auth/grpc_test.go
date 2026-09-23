package auth_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	authv1 "github.com/Briyantama/SELA/gen/go/auth/v1"
	"github.com/Briyantama/SELA/services/auth"
)

func startGRPC(t *testing.T, flow auth.Flow) authv1.AuthServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	authv1.RegisterAuthServiceServer(srv, auth.NewGRPCServer(flow))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return authv1.NewAuthServiceClient(conn)
}

func wantCode(t *testing.T, err error, want codes.Code) *status.Status {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok || st.Code() != want {
		t.Fatalf("err = %v, want gRPC code %s", err, want)
	}
	return st
}

func TestGRPCRequestOtp_sendsTheCode(t *testing.T) {
	// Arrange
	h := newHarness(t)
	client := startGRPC(t, h.svc)

	// Act
	resp, err := client.RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "host@example.test"})

	// Assert
	if err != nil {
		t.Fatalf("RequestOtp: %v", err)
	}
	if resp.GetExpiresInSeconds() != 300 {
		t.Errorf("expires_in_seconds = %d, want 300", resp.GetExpiresInSeconds())
	}
	if h.mailer.count() != 1 {
		t.Errorf("emails sent = %d, want 1", h.mailer.count())
	}
}

func TestGRPCVerifyOtp_returnsTheSessionForANewHost(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	client := startGRPC(t, h.svc)
	if _, err := client.RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "host@example.test"}); err != nil {
		t.Fatalf("RequestOtp: %v", err)
	}

	// Act
	resp, err := client.VerifyOtp(context.Background(), &authv1.VerifyOtpRequest{Email: "host@example.test", Code: "111111"})

	// Assert
	if err != nil {
		t.Fatalf("VerifyOtp: %v", err)
	}
	if len(resp.GetSessionToken()) < 43 || resp.GetHostId() == "" || !resp.GetIsNewHost() {
		t.Errorf("response = %+v", resp)
	}
	if resp.GetSessionExpiresInSeconds() != 43200 {
		t.Errorf("session_expires_in_seconds = %d, want 43200", resp.GetSessionExpiresInSeconds())
	}
	hostID, err := h.svc.Authenticate(context.Background(), resp.GetSessionToken())
	if err != nil || hostID != resp.GetHostId() {
		t.Errorf("the returned token must authenticate as the returned host: (%q, %v)", hostID, err)
	}
}

func TestGRPCErrors_mapToStatusCodes(t *testing.T) {
	t.Run("invalid email", func(t *testing.T) {
		// Arrange
		client := startGRPC(t, newHarness(t).svc)

		// Act
		_, err := client.RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "nope"})

		// Assert
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("wrong code", func(t *testing.T) {
		// Arrange
		h := newHarness(t, "111111")
		client := startGRPC(t, h.svc)
		if _, err := client.RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "host@example.test"}); err != nil {
			t.Fatalf("RequestOtp: %v", err)
		}

		// Act
		_, err := client.VerifyOtp(context.Background(), &authv1.VerifyOtpRequest{Email: "host@example.test", Code: "000000"})

		// Assert
		wantCode(t, err, codes.Unauthenticated)
	})

	t.Run("rate limited", func(t *testing.T) {
		// Arrange
		client := startGRPC(t, newHarness(t).svc)
		for i := range 3 {
			if _, err := client.RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "host@example.test"}); err != nil {
				t.Fatalf("request %d: %v", i, err)
			}
		}

		// Act
		_, err := client.RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "host@example.test"})

		// Assert
		st := wantCode(t, err, codes.ResourceExhausted)
		if !strings.Contains(st.Message(), "retry") {
			t.Errorf("message %q should tell the caller when to retry", st.Message())
		}
	})

	t.Run("delivery failure", func(t *testing.T) {
		// Arrange
		h := newHarness(t)
		h.mailer.err = errors.New("smtp password rejected")
		client := startGRPC(t, h.svc)

		// Act
		_, err := client.RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "host@example.test"})

		// Assert
		st := wantCode(t, err, codes.Unavailable)
		if strings.Contains(st.Message(), "smtp") {
			t.Errorf("message leaks delivery details: %q", st.Message())
		}
	})

	t.Run("internal errors are generic", func(t *testing.T) {
		// Arrange
		client := startGRPC(t, &stubFlow{
			requestErr: errors.New("redis at 10.0.0.5 refused the connection"),
			verifyErr:  errors.New("pq: password authentication failed"),
		})

		// Act
		_, reqErr := client.RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "host@example.test"})
		_, verErr := client.VerifyOtp(context.Background(), &authv1.VerifyOtpRequest{Email: "host@example.test", Code: "111111"})

		// Assert
		for _, err := range []error{reqErr, verErr} {
			st := wantCode(t, err, codes.Internal)
			if st.Message() != "internal error" {
				t.Errorf("message = %q, want the generic message", st.Message())
			}
		}
	})
}

func TestGRPCVerifyOtp_lockoutIsResourceExhausted(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	client := startGRPC(t, h.svc)
	if _, err := client.RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "host@example.test"}); err != nil {
		t.Fatalf("RequestOtp: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error

	// Act
	for range 5 {
		_, err = client.VerifyOtp(ctx, &authv1.VerifyOtpRequest{Email: "host@example.test", Code: "000000"})
	}

	// Assert
	wantCode(t, err, codes.ResourceExhausted)
}
