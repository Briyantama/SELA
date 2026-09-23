package media_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Briyantama/SELA/services/media"
)

func TestStartSession_createsAnAnonymousSessionForAnActiveEvent(t *testing.T) {
	// Arrange
	f := newFixture(t)
	eventID := f.event(t, eventOpts{shotLimit: limit(5), involvesMinors: true})

	// Act
	started, err := f.svc.StartSession(context.Background(), eventID, "", nil)

	// Assert
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if started.Token == "" || started.Resumed || started.Guest.EventID != eventID || started.Guest.SessionID == "" {
		t.Fatalf("started = %+v", started)
	}
	if started.ShotsRemaining == nil || *started.ShotsRemaining != 5 || started.ShotLimit == nil || *started.ShotLimit != 5 {
		t.Fatalf("shots = %v of %v, want 5 of 5", started.ShotsRemaining, started.ShotLimit)
	}
	if !started.InvolvesMinors || started.ExpiresIn != sessionTTL {
		t.Fatalf("minors = %t, expires in %v", started.InvolvesMinors, started.ExpiresIn)
	}
	var rows int
	_ = f.conn.QueryRow(`SELECT count(*) FROM guest_sessions WHERE session_id = $1 AND event_id = $2`,
		started.Guest.SessionID, eventID).Scan(&rows)
	if rows != 1 {
		t.Fatalf("guest_sessions rows = %d, want 1", rows)
	}
}

func TestStartSession_unlimitedEventsReportNoQuota(t *testing.T) {
	// Arrange
	f := newFixture(t)

	// Act
	started := f.guest(t, f.event(t, eventOpts{}))

	// Assert
	if started.ShotLimit != nil || started.ShotsRemaining != nil {
		t.Fatalf("shots = %v of %v, want nil for unlimited", started.ShotsRemaining, started.ShotLimit)
	}
}

func TestStartSession_resumesAValidSessionForTheSameEvent(t *testing.T) {
	// Arrange
	f := newFixture(t)
	eventID := f.event(t, eventOpts{})
	first := f.guest(t, eventID)

	// Act
	again, err := f.svc.StartSession(context.Background(), eventID, first.Token, nil)

	// Assert
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if !again.Resumed || again.Guest != first.Guest || again.Token != first.Token {
		t.Fatalf("again = %+v, want the first session resumed", again)
	}
}

func TestStartSession_aTokenFromAnotherEventStartsAFreshSession(t *testing.T) {
	// Arrange
	f := newFixture(t)
	other := f.guest(t, f.event(t, eventOpts{}))
	eventID := f.event(t, eventOpts{})

	// Act
	started, err := f.svc.StartSession(context.Background(), eventID, other.Token, nil)

	// Assert
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if started.Resumed || started.Guest.EventID != eventID || started.Token == other.Token {
		t.Fatalf("started = %+v, want a new session for %s", started, eventID)
	}
}

func TestStartSession_refusesEventsGuestsCannotJoin(t *testing.T) {
	f := newFixture(t)
	past := f.clock.Now().Add(-time.Minute)

	tests := []struct {
		name    string
		eventID string
	}{
		{"draft", f.event(t, eventOpts{status: "draft"})},
		{"expired status", f.event(t, eventOpts{status: "expired"})},
		{"past expiry", f.event(t, eventOpts{expiresAt: &past})},
		{"unknown", "0b8a1c7e-3c1f-4f6e-9a55-1d2b3c4d5e6f"},
		{"not a uuid", "'; DROP TABLE events; --"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, err := f.svc.StartSession(context.Background(), tc.eventID, "", nil)

			// Assert
			if !errors.Is(err, media.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestStartSession_validatesTheOptionalNickname(t *testing.T) {
	f := newFixture(t)
	eventID := f.event(t, eventOpts{})

	tests := []struct {
		name     string
		nickname string
		want     *string
		wantErr  bool
	}{
		{"trimmed", "  Budi  ", ptr("Budi"), false},
		{"blank means none", "   ", nil, false},
		{"forty characters", strings.Repeat("a", 40), ptr(strings.Repeat("a", 40)), false},
		{"too long", strings.Repeat("a", 41), nil, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			started, err := f.svc.StartSession(context.Background(), eventID, "", &tc.nickname)

			// Assert
			var verr *media.ValidationError
			if tc.wantErr {
				if !errors.As(err, &verr) || verr.Field != "nickname" {
					t.Fatalf("err = %v, want a nickname ValidationError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			var got *string
			_ = f.conn.QueryRow(`SELECT nickname FROM guest_sessions WHERE session_id = $1`, started.Guest.SessionID).Scan(&got)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("nickname = %v, want %v", deref(got), deref(tc.want))
			}
		})
	}
}

func TestAuthenticate_resolvesOnlyAValidTokenForItsOwnEvent(t *testing.T) {
	// Arrange
	f := newFixture(t)
	eventID := f.event(t, eventOpts{})
	started := f.guest(t, eventID)
	otherEvent := f.event(t, eventOpts{})
	ctx := context.Background()

	// Act
	guest, okErr := f.svc.Authenticate(ctx, eventID, started.Token)
	_, wrongEvent := f.svc.Authenticate(ctx, otherEvent, started.Token)
	_, bogus := f.svc.Authenticate(ctx, eventID, "not-a-token")
	_, empty := f.svc.Authenticate(ctx, eventID, "")

	// Assert
	if okErr != nil || guest != started.Guest {
		t.Fatalf("Authenticate = %+v, %v", guest, okErr)
	}
	for name, err := range map[string]error{"wrong event": wrongEvent, "bogus": bogus, "empty": empty} {
		if !errors.Is(err, media.ErrUnauthorized) {
			t.Errorf("%s: err = %v, want ErrUnauthorized", name, err)
		}
	}
}

func TestSessions_redisStoresOnlyTheTokenHashWithATTL(t *testing.T) {
	// Arrange
	f := newFixture(t)
	started := f.guest(t, f.event(t, eventOpts{}))

	// Act
	keys := f.keysWithPrefix("guest:")

	// Assert
	if len(keys) != 1 {
		t.Fatalf("guest keys = %v, want exactly one", keys)
	}
	for _, key := range f.mr.Keys() {
		if strings.Contains(key, started.Token) {
			t.Fatalf("redis key %q contains the raw token", key)
		}
		value, _ := f.mr.Get(key)
		if strings.Contains(value, started.Token) {
			t.Fatalf("redis value of %q contains the raw token", key)
		}
	}
	if ttl := f.mr.TTL(keys[0]); ttl <= 0 || ttl > sessionTTL {
		t.Fatalf("session TTL = %v, want (0, %v]", ttl, sessionTTL)
	}
}

func TestAuthenticate_expiresWithTheSession(t *testing.T) {
	// Arrange
	f := newFixture(t)
	eventID := f.event(t, eventOpts{})
	started := f.guest(t, eventID)
	f.mr.FastForward(sessionTTL + time.Second)

	// Act
	_, err := f.svc.Authenticate(context.Background(), eventID, started.Token)

	// Assert
	if !errors.Is(err, media.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized after the TTL", err)
	}
}

func ptr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
