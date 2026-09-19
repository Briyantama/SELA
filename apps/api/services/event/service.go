package event

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // bundle the zone database so Asia/Jakarta resolves on minimal images
	"unicode"
	"unicode/utf8"
)

const (
	defaultTimezone      = "Asia/Jakarta"
	statusActive         = "active"
	revealInstant        = "instant"
	revealDelayed        = "delayed"
	maxNameRunes         = 200
	maxRevealDelayHours  = 24 * 365 // one year; also keeps the arithmetic far from overflow
	maxShortCodeAttempts = 5
	shortCodeLength      = 8
	base62Alphabet       = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	accessTokenBytes     = 32
	dateLayout           = "2006-01-02"
)

var (
	// shortCodePattern is the exact shape of a generated short code (8 base62 characters).
	shortCodePattern = regexp.MustCompile(`^[0-9A-Za-z]{8}$`)

	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	// categoryOrder is the display order from FSD 4.1; unknown codes follow alphabetically.
	categoryOrder = map[string]int{"pernikahan": 0, "wisuda": 1, "ulang_tahun": 2, "gathering": 3, "reuni": 4, "lainnya": 5}
)

// Options configures the Service. ShortLinkBaseURL is required; the generators default to
// cryptographically random values and are injectable for tests.
type Options struct {
	// ShortLinkBaseURL is the public base (no trailing slash) that short links and QR codes point to.
	ShortLinkBaseURL string

	NewShortCode   func() (string, error)
	NewAccessToken func() (string, error)

	// Now supplies the current time for expiry checks. Defaults to time.Now; injectable for tests.
	Now func() time.Time
}

// Service implements the event use cases. Every method that touches a specific event takes the
// authenticated host id and can only see that host's events.
type Service struct {
	repo Repository
	opts Options
}

// NewService wires the use cases. It panics without a short-link base URL, because that is a
// deployment mistake that must never reach production silently.
func NewService(repo Repository, opts Options) *Service {
	if opts.ShortLinkBaseURL == "" {
		panic("event: Options.ShortLinkBaseURL is required")
	}
	if opts.NewShortCode == nil {
		opts.NewShortCode = randomShortCode
	}
	if opts.NewAccessToken == nil {
		opts.NewAccessToken = randomAccessToken
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{repo: repo, opts: opts}
}

// ListCategories returns the presets in display order with their effective defaults resolved.
func (s *Service) ListCategories(ctx context.Context) ([]Category, error) {
	cats, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	for i := range cats {
		resolveEffective(&cats[i])
	}
	sort.SliceStable(cats, func(i, j int) bool {
		ri, rj := displayRank(cats[i].Code), displayRank(cats[j].Code)
		if ri != rj {
			return ri < rj
		}
		return cats[i].Code < cats[j].Code
	})
	return cats, nil
}

func displayRank(code string) int {
	if rank, ok := categoryOrder[code]; ok {
		return rank
	}
	return len(categoryOrder)
}

// resolveEffective fills the effective defaults: a defined category default wins; anything the
// product has not defined ("tbd") falls back to unlimited shots and an instant reveal, matching the
// Free plan (BRD 7: no strict shot limits) and the common instant-reveal default (FSD 4.5).
func resolveEffective(c *Category) {
	c.EffectiveShotLimit = nil
	if c.ShotLimitDefault == "limited" && c.DefaultShotLimit != nil {
		v := *c.DefaultShotLimit
		c.EffectiveShotLimit = &v
	}

	c.EffectiveRevealMode = revealInstant
	c.EffectiveRevealDelayHours = nil
	if c.RevealDefault == revealDelayed && c.DefaultRevealDelayHours != nil {
		v := *c.DefaultRevealDelayHours
		c.EffectiveRevealMode = revealDelayed
		c.EffectiveRevealDelayHours = &v
	}
}

// CreateEvent validates the input, resolves settings (host override, then category default, then
// fallback), allocates a unique short code and stores the event for hostID.
func (s *Service) CreateEvent(ctx context.Context, hostID string, in CreateInput) (Event, error) {
	name, err := validateName(in.Name)
	if err != nil {
		return Event{}, err
	}
	date, err := validateDate(in.EventDate)
	if err != nil {
		return Event{}, err
	}
	code := strings.TrimSpace(in.CategoryCode)
	if code == "" {
		return Event{}, &ValidationError{"category_code is required"}
	}
	cat, err := s.repo.GetCategory(ctx, code)
	if errors.Is(err, ErrUnknownCategory) {
		return Event{}, &ValidationError{"unknown category"}
	}
	if err != nil {
		return Event{}, err
	}
	resolveEffective(&cat)

	shot, mode, delay, err := resolveSettings(cat, in)
	if err != nil {
		return Event{}, err
	}

	var revealAt *time.Time
	if mode == revealDelayed {
		at, err := revealTime(date, defaultTimezone, *delay)
		if err != nil {
			return Event{}, err
		}
		revealAt = &at
	}

	token, err := s.opts.NewAccessToken()
	if err != nil {
		return Event{}, fmt.Errorf("generate access token: %w", err)
	}

	for range maxShortCodeAttempts {
		shortCode, err := s.opts.NewShortCode()
		if err != nil {
			return Event{}, fmt.Errorf("generate short code: %w", err)
		}
		rec, err := s.repo.CreateEvent(ctx, NewEvent{
			HostID: hostID, CategoryCode: cat.Code, Name: name, EventDate: date.Format(dateLayout),
			Timezone: defaultTimezone, ShotLimit: shot, RevealMode: mode, RevealAt: revealAt,
			ShortCode: shortCode, AccessToken: token,
		})
		switch {
		case errors.Is(err, ErrShortCodeTaken):
			continue
		case errors.Is(err, ErrUnknownCategory):
			return Event{}, &ValidationError{"unknown category"}
		case err != nil:
			return Event{}, err
		}
		return s.toEvent(rec), nil
	}
	return Event{}, errors.New("could not allocate a unique short code")
}

// GetEvent returns the event only when it belongs to hostID; otherwise ErrNotFound.
func (s *Service) GetEvent(ctx context.Context, hostID, eventID string) (Event, error) {
	if !uuidPattern.MatchString(eventID) {
		return Event{}, ErrNotFound
	}
	rec, err := s.repo.GetEvent(ctx, hostID, eventID)
	if err != nil {
		return Event{}, err
	}
	return s.toEvent(rec), nil
}

// ResolveShortCode turns a short code into the guest-safe view of its event. It is public: the code
// itself is the shared secret. Anything that is not a live event returns ErrNotFound with no way to
// tell the reasons apart: a malformed code (rejected before any query), an unknown or wrongly-cased
// code, an event that is not active (draft or expired), or one whose expiry has passed.
func (s *Service) ResolveShortCode(ctx context.Context, code string) (PublicEvent, error) {
	if !shortCodePattern.MatchString(code) {
		return PublicEvent{}, ErrNotFound
	}
	rec, err := s.repo.GetEventByShortCode(ctx, code)
	if err != nil {
		return PublicEvent{}, err
	}
	if rec.Status != statusActive {
		return PublicEvent{}, ErrNotFound
	}
	if rec.ExpiresAt != nil && !s.opts.Now().Before(*rec.ExpiresAt) {
		return PublicEvent{}, ErrNotFound
	}
	return PublicEvent{
		EventID: rec.EventID, ShortCode: rec.ShortCode, Name: rec.Name, EventDate: rec.EventDate,
		Timezone: rec.Timezone, CategoryCode: rec.CategoryCode, ThemeKey: rec.ThemeKey, Status: rec.Status,
		ShotLimit: rec.ShotLimit, RevealMode: rec.RevealMode, RevealAt: rec.RevealAt,
	}, nil
}

// QRCode renders the event's short link as a QR image, for the owning host only.
func (s *Service) QRCode(ctx context.Context, hostID, eventID string, format QRFormat) ([]byte, string, error) {
	if format != QRPNG && format != QRSVG {
		return nil, "", ErrUnsupportedFormat
	}
	ev, err := s.GetEvent(ctx, hostID, eventID)
	if err != nil {
		return nil, "", err
	}
	return RenderQR(ev.ShortLink, format)
}

func (s *Service) toEvent(rec Record) Event {
	return Event{
		ID: rec.ID, CategoryCode: rec.CategoryCode, Name: rec.Name, EventDate: rec.EventDate,
		Timezone: rec.Timezone, Status: rec.Status, ShortCode: rec.ShortCode,
		ShortLink: s.opts.ShortLinkBaseURL + "/e/" + rec.ShortCode,
		QRPNGURL:  "/api/v1/events/" + rec.ID + "/qr.png",
		QRSVGURL:  "/api/v1/events/" + rec.ID + "/qr.svg",
		ShotLimit: rec.ShotLimit, RevealMode: rec.RevealMode, RevealAt: rec.RevealAt,
		Package: rec.Package, CreatedAt: rec.CreatedAt,
	}
}

func validateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", &ValidationError{"name is required"}
	case utf8.RuneCountInString(name) > maxNameRunes:
		return "", &ValidationError{fmt.Sprintf("name must be at most %d characters", maxNameRunes)}
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", &ValidationError{"name contains invalid characters"}
		}
	}
	return name, nil
}

func validateDate(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, &ValidationError{"event_date is required"}
	}
	date, err := time.Parse(dateLayout, raw)
	if err != nil {
		return time.Time{}, &ValidationError{"event_date must be a valid date (YYYY-MM-DD)"}
	}
	return date, nil
}

// resolveSettings applies host overrides on top of the category's effective defaults.
func resolveSettings(cat Category, in CreateInput) (shot *int, mode string, delay *int, err error) {
	switch {
	case in.ShotLimit == nil:
		shot = cat.EffectiveShotLimit
	case *in.ShotLimit < 0 || *in.ShotLimit > math.MaxInt32:
		return nil, "", nil, &ValidationError{"shot_limit must be 0 (unlimited) or a positive number"}
	case *in.ShotLimit > 0:
		v := *in.ShotLimit
		shot = &v
	}

	mode = cat.EffectiveRevealMode
	if in.RevealMode != nil {
		if *in.RevealMode != revealInstant && *in.RevealMode != revealDelayed {
			return nil, "", nil, &ValidationError{"reveal_mode must be instant or delayed"}
		}
		mode = *in.RevealMode
	}

	if in.RevealDelayHours != nil {
		switch {
		case *in.RevealDelayHours <= 0:
			return nil, "", nil, &ValidationError{"reveal_delay_hours must be positive"}
		case *in.RevealDelayHours > maxRevealDelayHours:
			return nil, "", nil, &ValidationError{fmt.Sprintf("reveal_delay_hours must be at most %d", maxRevealDelayHours)}
		}
	}

	if mode == revealInstant {
		if in.RevealDelayHours != nil {
			return nil, "", nil, &ValidationError{"reveal_delay_hours is only valid with a delayed reveal"}
		}
		return shot, mode, nil, nil
	}

	delay = in.RevealDelayHours
	if delay == nil {
		delay = cat.EffectiveRevealDelayHours
	}
	if delay == nil {
		return nil, "", nil, &ValidationError{"reveal_delay_hours is required for a delayed reveal"}
	}
	return shot, mode, delay, nil
}

// revealTime is the start of the event day in the event's time zone plus the delay.
func revealTime(date time.Time, timezone string, delayHours int) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load time zone %s: %w", timezone, err)
	}
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	return dayStart.Add(time.Duration(delayHours) * time.Hour), nil
}

// randomShortCode returns eight random base62 characters without modulo bias.
func randomShortCode() (string, error) {
	out := make([]byte, shortCodeLength)
	limit := big.NewInt(int64(len(base62Alphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[i] = base62Alphabet[n.Int64()]
	}
	return string(out), nil
}

// randomAccessToken returns 256 random bits, base64url encoded.
func randomAccessToken() (string, error) {
	buf := make([]byte, accessTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
