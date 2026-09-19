package event_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Briyantama/SELA/services/event"
)

var fixedNow = time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)

// serviceAt builds a service on the fixture's database with a fixed clock.
func (f *fixture) serviceAt(now time.Time) *event.Service {
	return event.NewService(f.repo, event.Options{ShortLinkBaseURL: baseURL, Now: func() time.Time { return now }})
}

func TestResolveShortCode_returnsTheGuestSafeEvent(t *testing.T) {
	// Arrange
	f := newFixture(t)
	created := mustCreate(t, f.svc, f.host(t, "owner@example.test"), event.CreateInput{
		CategoryCode: "ulang_tahun", Name: "Ulang Tahun Sela", EventDate: "2026-12-05", ShotLimit: ptr(20),
	})

	// Act
	got, err := f.svc.ResolveShortCode(context.Background(), created.ShortCode)

	// Assert
	if err != nil {
		t.Fatalf("ResolveShortCode: %v", err)
	}
	if got.EventID != created.ID || got.ShortCode != created.ShortCode || got.Name != "Ulang Tahun Sela" || got.EventDate != "2026-12-05" {
		t.Errorf("got %+v, want the created event", got)
	}
	if got.Timezone != "Asia/Jakarta" || got.CategoryCode != "ulang_tahun" || got.ThemeKey != "birthday" || got.Status != "active" {
		t.Errorf("got %+v", got)
	}
	if got.ShotLimit == nil || *got.ShotLimit != 20 || got.RevealMode != "instant" || got.RevealAt != nil {
		t.Errorf("settings = shot:%v reveal:%s at:%v", got.ShotLimit, got.RevealMode, got.RevealAt)
	}
}

func TestResolveShortCode_aDelayedEventExposesItsRevealTime(t *testing.T) {
	// Arrange
	f := newFixture(t)
	created := mustCreate(t, f.svc, f.host(t, "owner@example.test"), event.CreateInput{CategoryCode: "pernikahan", Name: "Akad", EventDate: "2026-12-05"})

	// Act
	got, err := f.svc.ResolveShortCode(context.Background(), created.ShortCode)

	// Assert
	if err != nil {
		t.Fatalf("ResolveShortCode: %v", err)
	}
	if got.RevealMode != "delayed" || got.RevealAt == nil || !got.RevealAt.Equal(*created.RevealAt) || got.ThemeKey != "wedding" {
		t.Errorf("got %+v, want the delayed reveal of the created event", got)
	}
}

func TestPublicEvent_hasNoOwnerOrBillingFields(t *testing.T) {
	// The guest-facing type must not be able to carry private data, whatever a handler does with it.
	typ := reflect.TypeOf(event.PublicEvent{})

	for _, name := range []string{"HostID", "AccessToken", "Package", "CreatedAt", "ExpiresAt"} {
		if _, found := typ.FieldByName(name); found {
			t.Errorf("PublicEvent must not have a %s field", name)
		}
	}
}

func TestResolveShortCode_rejectsMalformedCodesAsNotFound(t *testing.T) {
	tests := []struct{ name, code string }{
		{"empty", ""},
		{"too short", "AAAAAAA"},
		{"too long", "AAAAAAAAA"},
		{"space inside", "AAAA AAA"},
		{"symbol", "AAAAAAA!"},
		{"sql injection", "' OR '1'='1"},
		{"unicode", "ÅÅÅÅÅÅÅÅ"},
		{"null byte", "AAAAAAA\x00"},
		{"path traversal", "../../../"},
	}

	f := newFixture(t)
	mustCreate(t, f.serviceWithCodes("AAAAAAAA"), f.host(t, "owner@example.test"), birthday("Ada"))

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, err := f.svc.ResolveShortCode(context.Background(), tc.code)

			// Assert
			if !errors.Is(err, event.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestResolveShortCode_anUnknownWellFormedCodeIsNotFound(t *testing.T) {
	// Arrange
	f := newFixture(t)

	// Act
	_, err := f.svc.ResolveShortCode(context.Background(), "ZZZZZZZZ")

	// Assert
	if !errors.Is(err, event.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveShortCode_isCaseSensitive(t *testing.T) {
	// Arrange
	f := newFixture(t)
	mustCreate(t, f.serviceWithCodes("AbCdEfGh"), f.host(t, "owner@example.test"), birthday("Ada"))

	// Act
	exact, exactErr := f.svc.ResolveShortCode(context.Background(), "AbCdEfGh")
	_, lowerErr := f.svc.ResolveShortCode(context.Background(), "abcdefgh")

	// Assert
	if exactErr != nil || exact.ShortCode != "AbCdEfGh" {
		t.Errorf("exact = %+v, %v", exact, exactErr)
	}
	if !errors.Is(lowerErr, event.ErrNotFound) {
		t.Errorf("a different-cased code err = %v, want ErrNotFound", lowerErr)
	}
}

func TestResolveShortCode_eventsThatAreNotActiveAreNotFound(t *testing.T) {
	for _, status := range []string{"draft", "expired"} {
		t.Run(status, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			created := mustCreate(t, f.svc, f.host(t, "owner@example.test"), birthday("Ada"))
			if _, err := f.conn.Exec(`UPDATE events SET status = $1 WHERE event_id = $2`, status, created.ID); err != nil {
				t.Fatalf("set status: %v", err)
			}

			// Act
			_, err := f.svc.ResolveShortCode(context.Background(), created.ShortCode)

			// Assert
			if !errors.Is(err, event.ErrNotFound) {
				t.Fatalf("a %s event err = %v, want ErrNotFound", status, err)
			}
		})
	}
}

func TestResolveShortCode_honoursTheExpiryTime(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		wantFound bool
	}{
		{"expired an hour ago", fixedNow.Add(-time.Hour), false},
		{"expires exactly now", fixedNow, false},
		{"expires in an hour", fixedNow.Add(time.Hour), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			created := mustCreate(t, f.svc, f.host(t, "owner@example.test"), birthday("Ada"))
			if _, err := f.conn.Exec(`UPDATE events SET expires_at = $1 WHERE event_id = $2`, tc.expiresAt, created.ID); err != nil {
				t.Fatalf("set expiry: %v", err)
			}

			// Act
			_, err := f.serviceAt(fixedNow).ResolveShortCode(context.Background(), created.ShortCode)

			// Assert
			if tc.wantFound && err != nil {
				t.Fatalf("err = %v, want the event to resolve", err)
			}
			if !tc.wantFound && !errors.Is(err, event.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestResolveShortCode_needsNoExpiryToResolve(t *testing.T) {
	// Arrange: a free event has no expires_at until retention is implemented.
	f := newFixture(t)
	created := mustCreate(t, f.svc, f.host(t, "owner@example.test"), birthday("Ada"))

	// Act
	_, err := f.serviceAt(fixedNow.AddDate(10, 0, 0)).ResolveShortCode(context.Background(), created.ShortCode)

	// Assert
	if err != nil {
		t.Fatalf("an event without expires_at must keep resolving: %v", err)
	}
}

func TestResolveShortCode_reportsDatabaseFailuresAsInternalErrors(t *testing.T) {
	// Arrange
	f := newFixture(t)
	if err := f.conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Act
	_, err := f.svc.ResolveShortCode(context.Background(), "AAAAAAAA")

	// Assert
	if err == nil || errors.Is(err, event.ErrNotFound) {
		t.Fatalf("err = %v, want an internal error that is not ErrNotFound", err)
	}
}
