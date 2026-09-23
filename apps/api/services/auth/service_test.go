package auth_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Briyantama/SELA/services/auth"
)

var testKey = []byte("0123456789abcdef0123456789abcdef-test-only")

var sixDigits = regexp.MustCompile(`\b(\d{6})\b`)

type sentMail struct{ to, subject, body string }

type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
	err  error
}

func (m *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, sentMail{to, subject, body})
	return nil
}

func (m *fakeMailer) last(t *testing.T) sentMail {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		t.Fatal("no email was sent")
	}
	return m.sent[len(m.sent)-1]
}

func (m *fakeMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

type fakeHosts struct {
	mu  sync.Mutex
	ids map[string]string
	err error
}

func (h *fakeHosts) FindOrCreateByEmail(_ context.Context, email string) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		return "", false, h.err
	}
	if h.ids == nil {
		h.ids = map[string]string{}
	}
	if id, ok := h.ids[email]; ok {
		return id, false, nil
	}
	id := fmt.Sprintf("host-%d", len(h.ids)+1)
	h.ids[email] = id
	return id, true, nil
}

// codeQueue hands out predetermined codes so tests are deterministic.
type codeQueue struct {
	mu    sync.Mutex
	codes []string
}

func (q *codeQueue) next() (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.codes) == 0 {
		return "", errors.New("code queue exhausted")
	}
	c := q.codes[0]
	q.codes = q.codes[1:]
	return c, nil
}

type harness struct {
	svc    *auth.Service
	mr     *miniredis.Miniredis
	mailer *fakeMailer
	hosts  *fakeHosts
}

func newHarness(t *testing.T, codes ...string) *harness {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	h := &harness{mr: mr, mailer: &fakeMailer{}, hosts: &fakeHosts{}}
	opts := auth.Options{HMACKey: testKey}
	if len(codes) > 0 {
		opts.GenerateCode = (&codeQueue{codes: codes}).next
	}
	h.svc = auth.NewService(auth.NewRedisStore(rdb), h.hosts, h.mailer, opts)
	return h
}

func requestOTP(t *testing.T, h *harness, email string) {
	t.Helper()
	if _, err := h.svc.RequestOTP(context.Background(), email, "203.0.113.7"); err != nil {
		t.Fatalf("RequestOTP(%q): %v", email, err)
	}
}

func redisDump(mr *miniredis.Miniredis) string {
	var b strings.Builder
	for _, k := range mr.Keys() {
		v, _ := mr.Get(k)
		b.WriteString(k + "=" + v + "\n")
	}
	return b.String()
}

func keysWithPrefix(mr *miniredis.Miniredis, prefix string) []string {
	var out []string
	for _, k := range mr.Keys() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

func TestRequestOTP_emailsASixDigitCodeAndReportsItsLifetime(t *testing.T) {
	// Arrange
	h := newHarness(t)

	// Act
	res, err := h.svc.RequestOTP(context.Background(), "host@example.test", "203.0.113.7")

	// Assert
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	if res.ExpiresIn != 5*time.Minute {
		t.Errorf("ExpiresIn = %s, want 5m", res.ExpiresIn)
	}
	mail := h.mailer.last(t)
	if mail.to != "host@example.test" {
		t.Errorf("mail sent to %q", mail.to)
	}
	if !sixDigits.MatchString(mail.body) {
		t.Errorf("mail body has no 6-digit code: %q", mail.body)
	}
}

func TestRequestOTP_normalizesTheEmailAddress(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")

	// Act
	requestOTP(t, h, "  Host@Example.TEST ")
	_, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")

	// Assert
	if h.mailer.last(t).to != "host@example.test" {
		t.Errorf("mail sent to %q, want the lower-cased address", h.mailer.last(t).to)
	}
	if err != nil {
		t.Fatalf("a code requested for a differently-cased address must verify: %v", err)
	}
}

func TestRequestOTP_rejectsMalformedAddressesWithoutSendingMail(t *testing.T) {
	tests := []struct{ name, email string }{
		{"empty", ""},
		{"no at sign", "not-an-email"},
		{"no dot in domain", "a@b"},
		{"display name", "Host <host@example.test>"},
		{"header injection", "host@example.test\r\nBcc: victim@example.test"},
		{"space inside", "ho st@example.test"},
		{"too long", strings.Repeat("a", 250) + "@example.test"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h := newHarness(t)

			// Act
			_, err := h.svc.RequestOTP(context.Background(), tc.email, "203.0.113.7")

			// Assert
			if !errors.Is(err, auth.ErrInvalidEmail) {
				t.Fatalf("err = %v, want ErrInvalidEmail", err)
			}
			if h.mailer.count() != 0 {
				t.Error("an email was sent for an invalid address")
			}
		})
	}
}

func TestRequestOTP_storesOnlyAKeyedHashWithAFiveMinuteTTL(t *testing.T) {
	// Arrange
	h := newHarness(t, "482915")

	// Act
	requestOTP(t, h, "host@example.test")

	// Assert
	dump := redisDump(h.mr)
	for _, secret := range []string{"482915", "host@example.test"} {
		if strings.Contains(dump, secret) {
			t.Errorf("redis contains %q in plaintext:\n%s", secret, dump)
		}
	}
	keys := keysWithPrefix(h.mr, "otp:")
	if len(keys) != 1 {
		t.Fatalf("expected exactly one otp: key, got %v", keys)
	}
	if ttl := h.mr.TTL(keys[0]); ttl != 5*time.Minute {
		t.Errorf("otp TTL = %s, want 5m", ttl)
	}
}

func TestRequestOTP_aNewRequestInvalidatesTheOldCode(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111", "222222")
	requestOTP(t, h, "host@example.test")
	requestOTP(t, h, "host@example.test")

	// Act
	_, oldErr := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")
	_, newErr := h.svc.VerifyOTP(context.Background(), "host@example.test", "222222")

	// Assert
	if !errors.Is(oldErr, auth.ErrInvalidCode) {
		t.Errorf("old code err = %v, want ErrInvalidCode", oldErr)
	}
	if newErr != nil {
		t.Errorf("new code err = %v, want success", newErr)
	}
}

func TestRequestOTP_limitsRequestsPerEmail(t *testing.T) {
	// Arrange
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		requestOTP(t, h, "host@example.test")
	}

	// Act
	_, err := h.svc.RequestOTP(context.Background(), "host@example.test", "203.0.113.7")

	// Assert
	if !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("4th request err = %v, want ErrRateLimited", err)
	}
	var retry *auth.RetryAfterError
	if !errors.As(err, &retry) || retry.After <= 0 || retry.After > 10*time.Minute {
		t.Errorf("RetryAfter = %+v, want between 0 and 10m", retry)
	}
	if h.mailer.count() != 3 {
		t.Errorf("mails sent = %d, want 3", h.mailer.count())
	}

	// The window resets.
	h.mr.FastForward(10*time.Minute + time.Second)
	if _, err := h.svc.RequestOTP(context.Background(), "host@example.test", "203.0.113.7"); err != nil {
		t.Errorf("request after the window: %v", err)
	}
}

func TestRequestOTP_limitsRequestsPerClientAcrossAddresses(t *testing.T) {
	// Arrange
	h := newHarness(t)
	for i := 0; i < 10; i++ {
		if _, err := h.svc.RequestOTP(context.Background(), fmt.Sprintf("host%d@example.test", i), "198.51.100.9"); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	// Act
	_, sameClient := h.svc.RequestOTP(context.Background(), "another@example.test", "198.51.100.9")
	_, otherClient := h.svc.RequestOTP(context.Background(), "another@example.test", "198.51.100.10")

	// Assert
	if !errors.Is(sameClient, auth.ErrRateLimited) {
		t.Errorf("11th request from one client err = %v, want ErrRateLimited", sameClient)
	}
	if otherClient != nil {
		t.Errorf("a different client must not be limited: %v", otherClient)
	}
}

func TestRequestOTP_deliveryFailureLeavesNoUsableCode(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	h.mailer.err = errors.New("smtp down")

	// Act
	_, err := h.svc.RequestOTP(context.Background(), "host@example.test", "203.0.113.7")
	h.mailer.err = nil
	_, verifyErr := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")

	// Assert
	if !errors.Is(err, auth.ErrDelivery) {
		t.Fatalf("err = %v, want ErrDelivery", err)
	}
	if !errors.Is(verifyErr, auth.ErrInvalidCode) {
		t.Errorf("a code that was never delivered must not verify: %v", verifyErr)
	}
	if keys := keysWithPrefix(h.mr, "otp:"); len(keys) != 0 {
		t.Errorf("otp keys left behind: %v", keys)
	}
}

func TestVerifyOTP_signsANewHostInAndCreatesTheRecord(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	requestOTP(t, h, "host@example.test")

	// Act
	session, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")

	// Assert
	if err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	if session.HostID == "" || !session.IsNewHost {
		t.Errorf("session = %+v, want a new host id", session)
	}
	if len(session.Token) < 43 {
		t.Errorf("session token %q is too short to be 256 random bits", session.Token)
	}
	if session.ExpiresIn != 12*time.Hour {
		t.Errorf("ExpiresIn = %s, want 12h", session.ExpiresIn)
	}
}

func TestVerifyOTP_anExistingHostIsNotCreatedAgain(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111", "222222")
	requestOTP(t, h, "host@example.test")
	first, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	requestOTP(t, h, "host@example.test")

	// Act
	second, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "222222")

	// Assert
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if second.IsNewHost || second.HostID != first.HostID {
		t.Errorf("second = %+v, want the same host and IsNewHost=false", second)
	}
	if second.Token == first.Token {
		t.Error("every sign-in must get a fresh session token")
	}
}

func TestVerifyOTP_aCodeWorksOnlyOnce(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	requestOTP(t, h, "host@example.test")
	if _, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111"); err != nil {
		t.Fatalf("first use: %v", err)
	}

	// Act
	_, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")

	// Assert
	if !errors.Is(err, auth.ErrInvalidCode) {
		t.Fatalf("reuse err = %v, want ErrInvalidCode", err)
	}
}

func TestVerifyOTP_rejectsWrongMissingExpiredAndMalformedCodes(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness)
		code  string
	}{
		{"wrong code", func(h *harness) { requestOTP(t, h, "host@example.test") }, "999999"},
		{"no code was requested", func(h *harness) {}, "111111"},
		{"expired code", func(h *harness) {
			requestOTP(t, h, "host@example.test")
			h.mr.FastForward(5*time.Minute + time.Second)
		}, "111111"},
		{"too short", func(h *harness) { requestOTP(t, h, "host@example.test") }, "11111"},
		{"too long", func(h *harness) { requestOTP(t, h, "host@example.test") }, "1111111"},
		{"not digits", func(h *harness) { requestOTP(t, h, "host@example.test") }, "11111a"},
		{"empty", func(h *harness) { requestOTP(t, h, "host@example.test") }, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h := newHarness(t, "111111")
			tc.setup(h)

			// Act
			_, err := h.svc.VerifyOTP(context.Background(), "host@example.test", tc.code)

			// Assert
			if !errors.Is(err, auth.ErrInvalidCode) {
				t.Fatalf("err = %v, want ErrInvalidCode", err)
			}
			if len(h.hosts.ids) != 0 {
				t.Error("a host was created for a failed verification")
			}
		})
	}
}

func TestVerifyOTP_rejectsAMalformedEmail(t *testing.T) {
	// Arrange
	h := newHarness(t)

	// Act
	_, err := h.svc.VerifyOTP(context.Background(), "nope", "111111")

	// Assert
	if !errors.Is(err, auth.ErrInvalidEmail) {
		t.Fatalf("err = %v, want ErrInvalidEmail", err)
	}
}

func TestVerifyOTP_locksTheAddressAfterFiveWrongAttempts(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111", "222222")
	requestOTP(t, h, "host@example.test")
	for i := 1; i <= 4; i++ {
		if _, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "000000"); !errors.Is(err, auth.ErrInvalidCode) {
			t.Fatalf("wrong attempt %d err = %v, want ErrInvalidCode", i, err)
		}
	}

	// Act
	_, fifth := h.svc.VerifyOTP(context.Background(), "host@example.test", "000000")
	_, correctWhileLocked := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")
	_, requestWhileLocked := h.svc.RequestOTP(context.Background(), "host@example.test", "203.0.113.7")

	// Assert
	if !errors.Is(fifth, auth.ErrLocked) {
		t.Errorf("5th wrong attempt err = %v, want ErrLocked", fifth)
	}
	if !errors.Is(correctWhileLocked, auth.ErrLocked) {
		t.Errorf("correct code while locked err = %v, want ErrLocked", correctWhileLocked)
	}
	if !errors.Is(requestWhileLocked, auth.ErrLocked) {
		t.Errorf("new request while locked err = %v, want ErrLocked", requestWhileLocked)
	}
	var retry *auth.RetryAfterError
	if !errors.As(correctWhileLocked, &retry) || retry.After <= 0 || retry.After > 15*time.Minute {
		t.Errorf("RetryAfter = %+v, want between 0 and 15m", retry)
	}

	// The lock expires after 15 minutes and a fresh code works again.
	h.mr.FastForward(15*time.Minute + time.Second)
	requestOTP(t, h, "host@example.test")
	if _, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "222222"); err != nil {
		t.Errorf("sign-in after the lock expired: %v", err)
	}
}

func TestVerifyOTP_concurrentAttemptsWithTheRightCodeSucceedExactlyOnce(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	requestOTP(t, h, "host@example.test")
	const attempts = 4
	results := make(chan error, attempts)

	// Act
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	// Assert
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, auth.ErrInvalidCode) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful verifications = %d, want exactly 1", successes)
	}
}

func TestAuthenticate_resolvesTheHostFromASessionToken(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	requestOTP(t, h, "host@example.test")
	session, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	// Act
	hostID, authErr := h.svc.Authenticate(context.Background(), session.Token)

	// Assert
	if authErr != nil || hostID != session.HostID {
		t.Fatalf("Authenticate = (%q, %v), want (%q, nil)", hostID, authErr, session.HostID)
	}
	if strings.Contains(redisDump(h.mr), session.Token) {
		t.Error("the session token is stored in plaintext; only a hash may be stored")
	}
	if keys := keysWithPrefix(h.mr, "session:"); len(keys) != 1 {
		t.Errorf("expected one session: key, got %v", keys)
	}
}

func TestAuthenticate_rejectsUnknownEmptyAndExpiredTokens(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	requestOTP(t, h, "host@example.test")
	session, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	h.mr.FastForward(12*time.Hour + time.Second)

	tests := []struct{ name, token string }{
		{"empty", ""},
		{"unknown", "not-a-real-token"},
		{"expired", session.Token},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, err := h.svc.Authenticate(context.Background(), tc.token)

			// Assert
			if !errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf("err = %v, want ErrUnauthenticated", err)
			}
		})
	}
}

func TestServiceErrors_areReportedWhenDependenciesFail(t *testing.T) {
	// Arrange
	h := newHarness(t, "111111")
	requestOTP(t, h, "host@example.test")
	h.hosts.err = errors.New("db down")

	// Act
	_, err := h.svc.VerifyOTP(context.Background(), "host@example.test", "111111")

	// Assert
	if err == nil || errors.Is(err, auth.ErrInvalidCode) {
		t.Fatalf("err = %v, want an internal error that is not ErrInvalidCode", err)
	}
}

func TestDefaultCodes_areSixRandomDigits(t *testing.T) {
	// Arrange
	h := newHarness(t)
	seen := map[string]bool{}

	// Act
	for i := 0; i < 8; i++ {
		requestOTP(t, h, fmt.Sprintf("host%d@example.test", i))
		m := sixDigits.FindString(h.mailer.last(t).body)
		if m == "" {
			t.Fatalf("no 6-digit code in %q", h.mailer.last(t).body)
		}
		seen[m] = true
	}

	// Assert
	if len(seen) < 2 {
		t.Errorf("8 requests produced only %d distinct codes", len(seen))
	}
}
