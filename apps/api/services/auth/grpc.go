package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	authv1 "github.com/Briyantama/SELA/gen/go/auth/v1"
)

// GRPCServer exposes the sign-in flow as the sela.auth.v1.AuthService gRPC service.
type GRPCServer struct {
	authv1.UnimplementedAuthServiceServer
	flow Flow
}

// NewGRPCServer returns the gRPC adapter for the flow.
func NewGRPCServer(flow Flow) *GRPCServer {
	return &GRPCServer{flow: flow}
}

// RequestOtp emails a one-time code to the address in the request.
func (s *GRPCServer) RequestOtp(ctx context.Context, req *authv1.RequestOtpRequest) (*authv1.RequestOtpResponse, error) {
	res, err := s.flow.RequestOTP(ctx, req.GetEmail(), peerAddr(ctx))
	if err != nil {
		return nil, grpcError(err)
	}
	return &authv1.RequestOtpResponse{ExpiresInSeconds: int32(seconds(res.ExpiresIn))}, nil
}

// VerifyOtp checks the code and returns a session. gRPC callers receive the token in the response;
// the HTTP API delivers it as a cookie instead.
func (s *GRPCServer) VerifyOtp(ctx context.Context, req *authv1.VerifyOtpRequest) (*authv1.VerifyOtpResponse, error) {
	session, err := s.flow.VerifyOTP(ctx, req.GetEmail(), req.GetCode())
	if err != nil {
		return nil, grpcError(err)
	}
	return &authv1.VerifyOtpResponse{
		SessionToken:            session.Token,
		HostId:                  session.HostID,
		IsNewHost:               session.IsNewHost,
		SessionExpiresInSeconds: int32(seconds(session.ExpiresIn)),
	}, nil
}

// grpcError maps a flow error to a status code with a fixed message. Internal details never reach the caller.
func grpcError(err error) error {
	var retry *RetryAfterError
	switch {
	case errors.Is(err, ErrInvalidEmail):
		return status.Error(codes.InvalidArgument, msgInvalidEmail)
	case errors.Is(err, ErrInvalidCode):
		return status.Error(codes.Unauthenticated, msgInvalidCode)
	case errors.Is(err, ErrUnauthenticated):
		return status.Error(codes.Unauthenticated, msgUnauthorized)
	case errors.Is(err, ErrRateLimited), errors.Is(err, ErrLocked):
		message := msgRateLimited
		if errors.Is(err, ErrLocked) {
			message = msgLocked
		}
		if errors.As(err, &retry) {
			message = fmt.Sprintf("%s; retry in %ds", message, max(seconds(retry.After), 1))
		}
		return status.Error(codes.ResourceExhausted, message)
	case errors.Is(err, ErrDelivery):
		slog.Warn("otp delivery failed", "error", err)
		return status.Error(codes.Unavailable, msgDelivery)
	default:
		slog.Error("auth request failed", "error", err)
		return status.Error(codes.Internal, msgInternal)
	}
}

// peerAddr identifies the caller by the connection address for rate limiting.
func peerAddr(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}
	return host
}
