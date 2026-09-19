package auth

import "context"

// Flow is the sign-in behavior the transport adapters (HTTP and gRPC) depend on.
// *Service implements it; tests substitute stubs to force specific outcomes.
type Flow interface {
	RequestOTP(ctx context.Context, email, client string) (RequestResult, error)
	VerifyOTP(ctx context.Context, email, code string) (Session, error)
	Authenticate(ctx context.Context, token string) (hostID string, err error)
}

// Compile-time check that Service satisfies Flow.
var _ Flow = (*Service)(nil)
