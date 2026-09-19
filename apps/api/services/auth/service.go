// Package auth signs event hosts in with an email one-time code.
//
// The flow (FSD 4.1, 8.6): RequestOTP emails a 6-digit code that lives in Redis for five minutes,
// stored only as a keyed hash. VerifyOTP checks it once, creates the host on first sign-in and
// returns an opaque session token whose hash is what Redis keeps. Requests are rate limited per
// address and per client, and repeated wrong guesses lock the address.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"
	"unicode"
)

const (
	codeDigits         = 6
	maxEmailLength     = 254
	sessionTokenBytes  = 32
	defaultOTPTTL      = 5 * time.Minute
	defaultLockout     = 15 * time.Minute
	defaultWindow      = 10 * time.Minute
	defaultSessionTTL  = 12 * time.Hour
	defaultMaxAttempts = 5
	defaultEmailLimit  = 3
	defaultClientLimit = 10
)

// Options tunes the service. Zero values select the defaults above; HMACKey is required.
type Options struct {
	// HMACKey keys the hashes of codes, emails, clients and tokens. Use at least 32 random bytes.
	HMACKey []byte

	OTPTTL             time.Duration
	LockDuration       time.Duration
	RequestWindow      time.Duration
	SessionTTL         time.Duration
	MaxAttempts        int
	EmailRequestLimit  int
	ClientRequestLimit int

	// GenerateCode returns a 6-digit code. Defaults to a cryptographically random one; tests inject fixed codes.
	GenerateCode func() (string, error)
}

func (o Options) withDefaults() Options {
	if o.OTPTTL == 0 {
		o.OTPTTL = defaultOTPTTL
	}
	if o.LockDuration == 0 {
		o.LockDuration = defaultLockout
	}
	if o.RequestWindow == 0 {
		o.RequestWindow = defaultWindow
	}
	if o.SessionTTL == 0 {
		o.SessionTTL = defaultSessionTTL
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = defaultMaxAttempts
	}
	if o.EmailRequestLimit == 0 {
		o.EmailRequestLimit = defaultEmailLimit
	}
	if o.ClientRequestLimit == 0 {
		o.ClientRequestLimit = defaultClientLimit
	}
	if o.GenerateCode == nil {
		o.GenerateCode = randomCode
	}
	return o
}

// Service implements the host sign-in flow.
type Service struct {
	store  Store
	hosts  HostRepository
	mailer Mailer
	opts   Options
}

// NewService wires the flow. It panics when no HMAC key is supplied, because that is a
// deployment mistake that must never reach production silently.
func NewService(store Store, hosts HostRepository, mailer Mailer, opts Options) *Service {
	if len(opts.HMACKey) == 0 {
		panic("auth: Options.HMACKey is required")
	}
	return &Service{store: store, hosts: hosts, mailer: mailer, opts: opts.withDefaults()}
}

// RequestResult reports how long the emailed code stays valid.
type RequestResult struct {
	ExpiresIn time.Duration
}

// Session is the outcome of a successful sign-in.
type Session struct {
	Token     string
	HostID    string
	IsNewHost bool
	ExpiresIn time.Duration
}

// RequestOTP emails a fresh code to the address. client identifies the caller (for example its IP)
// for rate limiting. The outcome never reveals whether the address is already registered.
func (s *Service) RequestOTP(ctx context.Context, email, client string) (RequestResult, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return RequestResult{}, err
	}
	emailID := s.id("email", email)

	if err := s.checkLock(ctx, emailID); err != nil {
		return RequestResult{}, err
	}
	if err := s.checkWindow(ctx, "rl:email:"+emailID, s.opts.EmailRequestLimit); err != nil {
		return RequestResult{}, err
	}
	if err := s.checkWindow(ctx, "rl:client:"+s.id("client", client), s.opts.ClientRequestLimit); err != nil {
		return RequestResult{}, err
	}

	code, err := s.opts.GenerateCode()
	if err != nil {
		return RequestResult{}, fmt.Errorf("generate code: %w", err)
	}
	if !isSixDigits(code) {
		return RequestResult{}, errors.New("generate code: not a 6-digit code")
	}

	if err := s.store.SaveCode(ctx, emailID, s.codeMAC(email, code), s.opts.OTPTTL); err != nil {
		return RequestResult{}, err
	}
	if err := s.mailer.Send(ctx, email, "Kode masuk Sela", s.emailBody(code)); err != nil {
		if delErr := s.store.DeleteCode(ctx, emailID); delErr != nil {
			return RequestResult{}, errors.Join(fmt.Errorf("%w: %w", ErrDelivery, err), delErr)
		}
		return RequestResult{}, fmt.Errorf("%w: %w", ErrDelivery, err)
	}
	return RequestResult{ExpiresIn: s.opts.OTPTTL}, nil
}

// VerifyOTP checks a code once. A correct code is consumed, the host is created on first sign-in,
// and a new session is returned. Wrong, missing and expired codes are indistinguishable.
func (s *Service) VerifyOTP(ctx context.Context, email, code string) (Session, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return Session{}, err
	}
	if !isSixDigits(code) {
		return Session{}, ErrInvalidCode
	}
	emailID := s.id("email", email)

	if err := s.checkLock(ctx, emailID); err != nil {
		return Session{}, err
	}

	stored, found, err := s.store.LoadCode(ctx, emailID)
	if err != nil {
		return Session{}, err
	}
	if !found {
		// Nothing outstanding to guess, so nothing to count: an attacker cannot lock an
		// address that has not requested a code.
		return Session{}, ErrInvalidCode
	}

	attempts, err := s.store.RecordAttempt(ctx, emailID, s.opts.OTPTTL)
	if err != nil {
		return Session{}, err
	}
	if attempts > int64(s.opts.MaxAttempts) {
		return Session{}, s.lock(ctx, emailID)
	}

	if subtle.ConstantTimeCompare([]byte(stored), []byte(s.codeMAC(email, code))) != 1 {
		if attempts >= int64(s.opts.MaxAttempts) {
			return Session{}, s.lock(ctx, emailID)
		}
		return Session{}, ErrInvalidCode
	}

	consumed, err := s.store.ConsumeCode(ctx, emailID)
	if err != nil {
		return Session{}, err
	}
	if !consumed {
		return Session{}, ErrInvalidCode // another request used this code first
	}

	hostID, created, err := s.hosts.FindOrCreateByEmail(ctx, email)
	if err != nil {
		return Session{}, fmt.Errorf("find or create host: %w", err)
	}

	token, err := newToken()
	if err != nil {
		return Session{}, err
	}
	if err := s.store.SaveSession(ctx, s.id("session", token), hostID, s.opts.SessionTTL); err != nil {
		return Session{}, err
	}
	return Session{Token: token, HostID: hostID, IsNewHost: created, ExpiresIn: s.opts.SessionTTL}, nil
}

// Authenticate resolves a session token to the host it belongs to.
func (s *Service) Authenticate(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrUnauthenticated
	}
	hostID, found, err := s.store.LoadSession(ctx, s.id("session", token))
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrUnauthenticated
	}
	return hostID, nil
}

func (s *Service) checkLock(ctx context.Context, emailID string) error {
	remaining, err := s.store.LockRemaining(ctx, emailID, s.opts.LockDuration)
	if err != nil {
		return err
	}
	if remaining > 0 {
		return &RetryAfterError{Err: ErrLocked, After: remaining}
	}
	return nil
}

func (s *Service) checkWindow(ctx context.Context, key string, limit int) error {
	count, remaining, err := s.store.CountWindow(ctx, key, s.opts.RequestWindow)
	if err != nil {
		return err
	}
	if count > int64(limit) {
		return &RetryAfterError{Err: ErrRateLimited, After: remaining}
	}
	return nil
}

// lock locks the address, discards its code and reports the lockout to the caller.
func (s *Service) lock(ctx context.Context, emailID string) error {
	if err := s.store.Lock(ctx, emailID, s.opts.LockDuration); err != nil {
		return err
	}
	if err := s.store.DeleteCode(ctx, emailID); err != nil {
		return err
	}
	return &RetryAfterError{Err: ErrLocked, After: s.opts.LockDuration}
}

func (s *Service) emailBody(code string) string {
	minutes := int(s.opts.OTPTTL / time.Minute)
	return fmt.Sprintf(
		"Kode masuk Sela Anda: %s\n\nKode berlaku %d menit dan hanya bisa dipakai sekali.\n"+
			"Jangan bagikan kode ini kepada siapa pun. Jika Anda tidak memintanya, abaikan email ini.\n",
		code, minutes)
}

// id derives a storage key component from a value, so Redis never holds the raw value.
func (s *Service) id(kind, value string) string {
	return s.hmacHex(kind, value)
}

func (s *Service) codeMAC(email, code string) string {
	return s.hmacHex("otp", email+"\x00"+code)
}

func (s *Service) hmacHex(domain, value string) string {
	mac := hmac.New(sha256.New, s.opts.HMACKey)
	mac.Write([]byte(domain))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// normalizeEmail trims and lower-cases the address and accepts only a plain, single addr-spec.
func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > maxEmailLength {
		return "", ErrInvalidEmail
	}
	for _, r := range email {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", ErrInvalidEmail
		}
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return "", ErrInvalidEmail
	}
	domain := email[strings.LastIndex(email, "@")+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

func isSixDigits(code string) bool {
	if len(code) != codeDigits {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

func randomCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", codeDigits, n.Int64()), nil
}

func newToken() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
