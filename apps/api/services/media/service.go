package media

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Briyantama/SELA/internal/objstore"
)

// Errors the adapters map to status codes, beside ErrNotFound and ErrUnsupportedType.
var (
	// ErrUnauthorized means the request carries no valid guest session for the event.
	ErrUnauthorized = errors.New("guest session required")
	// ErrQuotaExhausted means the guest's camera is full (FR-03.3).
	ErrQuotaExhausted = errors.New("shot limit reached")
	// ErrTooLarge means the declared upload exceeds the size limit (FR-04.1).
	ErrTooLarge = errors.New("file too large")
	// ErrUploadMissing means completion was asked for before the bytes reached storage.
	ErrUploadMissing = errors.New("upload not received")
	// ErrRejected means the uploaded bytes failed validation or stripping (FR-SEC.2, FR-SEC.3).
	ErrRejected = errors.New("upload rejected")
)

// ValidationError reports a bad input field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

const (
	// MaxPhotoBytes and MaxVideoBytes are the upload size limits (product decision, Milestone 2).
	MaxPhotoBytes int64 = 20 << 20
	MaxVideoBytes int64 = 200 << 20 // videos are not accepted until their metadata can be stripped

	defaultUploadTTL   = 10 * time.Minute // within FSD 8.5's 5–15 minutes
	defaultDownloadTTL = 5 * time.Minute
	// sweepGrace lets an upload that started just before its URL expired still finish.
	sweepGrace = 5 * time.Minute

	maxNicknameLen = 40
	tokenBytes     = 32
)

// uploadable lists the types that can be stripped of metadata today. HEIC and video need a
// metadata-rewriting tool first, so they are refused rather than stored unstripped.
var uploadable = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// Guest identifies an authenticated guest session within its event.
type Guest struct {
	EventID   string
	SessionID string
}

// StartedSession is the result of joining an event. Token goes only into the HttpOnly cookie.
type StartedSession struct {
	Token          string
	Guest          Guest
	Resumed        bool
	ShotLimit      *int // nil = unlimited
	ShotsRemaining *int // nil = unlimited
	InvolvesMinors bool
	ExpiresIn      time.Duration
}

// Upload is an issued upload slot: the browser PUTs the bytes with Request, then asks to complete.
type Upload struct {
	MediaID        string
	Request        objstore.PresignedRequest
	ShotsRemaining *int
}

// Item is a media row as a caller may see it; URL is a short-lived download link for ready items.
type Item struct {
	Media
	URL string
}

// Options configures the service.
type Options struct {
	// HMACKey keys the hash under which guest tokens are stored.
	HMACKey []byte
	// SessionTTL bounds a guest session and its shot counter.
	SessionTTL time.Duration
	// UploadTTL is the life of a pre-signed upload URL.
	UploadTTL time.Duration
	// DownloadTTL is the life of a pre-signed download URL.
	DownloadTTL time.Duration
	Now         func() time.Time
}

// Service implements guest sessions, uploads and the galleries.
type Service struct {
	repo   Repository
	guests GuestStore
	store  objstore.Store
	opts   Options
}

// NewService wires the service; HMACKey and SessionTTL are required.
func NewService(repo Repository, guests GuestStore, store objstore.Store, opts Options) (*Service, error) {
	if len(opts.HMACKey) == 0 || opts.SessionTTL <= 0 {
		return nil, errors.New("media: HMACKey and SessionTTL are required")
	}
	if opts.UploadTTL <= 0 {
		opts.UploadTTL = defaultUploadTTL
	}
	if opts.DownloadTTL <= 0 {
		opts.DownloadTTL = defaultDownloadTTL
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{repo: repo, guests: guests, store: store, opts: opts}, nil
}

// StartSession joins an active event as an anonymous guest (FR-02.1, FR-02.2), resuming the session
// behind token when it belongs to the same event.
func (s *Service) StartSession(ctx context.Context, eventID, token string, nickname *string) (StartedSession, error) {
	if !isUUID(eventID) {
		return StartedSession{}, ErrNotFound
	}
	nick, err := normalizeNickname(nickname)
	if err != nil {
		return StartedSession{}, err
	}
	ev, err := s.repo.GuestEvent(ctx, eventID, s.opts.Now())
	if err != nil {
		return StartedSession{}, err
	}

	started := StartedSession{ShotLimit: ev.ShotLimit, InvolvesMinors: ev.InvolvesMinors, ExpiresIn: s.opts.SessionTTL}
	if token != "" {
		guest, ok, err := s.guests.LoadSession(ctx, s.tokenID(token))
		if err != nil {
			return StartedSession{}, err
		}
		if ok && guest.EventID == eventID {
			started.Token, started.Guest, started.Resumed = token, guest, true
			return s.withRemaining(ctx, started)
		}
	}

	sessionID, err := s.repo.CreateSession(ctx, eventID, nick)
	if err != nil {
		return StartedSession{}, err
	}
	token, err = newToken()
	if err != nil {
		return StartedSession{}, err
	}
	guest := Guest{EventID: eventID, SessionID: sessionID}
	if err := s.guests.SaveSession(ctx, s.tokenID(token), guest, s.opts.SessionTTL); err != nil {
		return StartedSession{}, err
	}
	started.Token, started.Guest = token, guest
	return s.withRemaining(ctx, started)
}

// Authenticate resolves a guest token for one event; any mismatch is ErrUnauthorized.
func (s *Service) Authenticate(ctx context.Context, eventID, token string) (Guest, error) {
	if token == "" {
		return Guest{}, ErrUnauthorized
	}
	guest, ok, err := s.guests.LoadSession(ctx, s.tokenID(token))
	if err != nil {
		return Guest{}, err
	}
	if !ok || guest.EventID != eventID {
		return Guest{}, ErrUnauthorized
	}
	return guest, nil
}

func (s *Service) withRemaining(ctx context.Context, started StartedSession) (StartedSession, error) {
	if started.ShotLimit == nil {
		return started, nil
	}
	remaining, err := s.remaining(ctx, started.Guest, *started.ShotLimit)
	if err != nil {
		return StartedSession{}, err
	}
	started.ShotsRemaining = &remaining
	return started, nil
}

// remaining derives the allowance from the durable media rows, the source of truth behind Redis.
func (s *Service) remaining(ctx context.Context, g Guest, limit int) (int, error) {
	used, err := s.repo.CountShots(ctx, g.EventID, g.SessionID)
	if err != nil {
		return 0, err
	}
	return max(limit-used, 0), nil
}

// tokenID is what Redis stores for a token: its keyed hash, never the token itself.
func (s *Service) tokenID(token string) string {
	mac := hmac.New(sha256.New, s.opts.HMACKey)
	mac.Write([]byte("guest\x00" + token))
	return hex.EncodeToString(mac.Sum(nil))
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate guest token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func normalizeNickname(nickname *string) (*string, error) {
	if nickname == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*nickname)
	if trimmed == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(trimmed) > maxNicknameLen {
		return nil, &ValidationError{Field: "nickname", Message: fmt.Sprintf("must be at most %d characters", maxNicknameLen)}
	}
	return &trimmed, nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isUUID screens ids before they reach a query, so malformed ids look exactly like unknown ones.
func isUUID(id string) bool { return uuidPattern.MatchString(id) }

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
