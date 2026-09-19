package auth

import (
	"errors"
	"time"
)

// Sentinel errors returned by the Service. Adapters map them to transport status codes.
var (
	ErrInvalidEmail    = errors.New("invalid email address")
	ErrInvalidCode     = errors.New("invalid or expired code")
	ErrRateLimited     = errors.New("too many requests")
	ErrLocked          = errors.New("too many failed attempts")
	ErrDelivery        = errors.New("could not deliver the code")
	ErrUnauthenticated = errors.New("not signed in")
)

// RetryAfterError wraps ErrRateLimited or ErrLocked with how long the caller should wait.
type RetryAfterError struct {
	Err   error
	After time.Duration
}

func (e *RetryAfterError) Error() string { return e.Err.Error() }

func (e *RetryAfterError) Unwrap() error { return e.Err }
