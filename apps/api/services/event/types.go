// Package event creates and reads the events hosts own: category presets, event creation with a
// unique short link and QR code, and host-scoped retrieval (FSD 4.1, 5, 6).
package event

import (
	"errors"
	"time"
)

// Sentinel errors returned by the Service. Adapters map them to transport status codes.
var (
	// ErrNotFound covers a missing event and an event owned by another host, so callers cannot tell them apart.
	ErrNotFound          = errors.New("event not found")
	ErrUnsupportedFormat = errors.New("unsupported QR format")

	// ErrShortCodeTaken and ErrUnknownCategory are reported by a Repository.
	ErrShortCodeTaken  = errors.New("short code already in use")
	ErrUnknownCategory = errors.New("unknown category")
)

// ValidationError is a problem with the caller's input. Its message is safe to show to the caller.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// Category is a preset (theme plus default settings), never a feature gate.
type Category struct {
	Code     string
	Name     string
	ThemeKey string

	// Raw defaults as stored: "tbd" means the product has not defined the value yet.
	ShotLimitDefault        string // unlimited | limited | tbd
	DefaultShotLimit        *int
	RevealDefault           string // instant | delayed | tbd
	DefaultRevealDelayHours *int

	// Effective defaults a new event gets when the host overrides nothing: the category default when
	// defined, otherwise the safe fallback (unlimited shots, instant reveal).
	EffectiveShotLimit        *int // nil = unlimited
	EffectiveRevealMode       string
	EffectiveRevealDelayHours *int
}

// Event is an event as returned to its owner. It never carries the host id or the access token.
type Event struct {
	ID           string
	CategoryCode string
	Name         string
	EventDate    string // YYYY-MM-DD
	Timezone     string
	Status       string
	ShortCode    string
	ShortLink    string
	QRPNGURL     string
	QRSVGURL     string
	ShotLimit    *int // nil = unlimited
	RevealMode   string
	RevealAt     *time.Time
	Package      string
	CreatedAt    time.Time
}

// CreateInput is what a host supplies. Pointer fields are optional overrides: nil means "use the default".
type CreateInput struct {
	CategoryCode string
	Name         string
	EventDate    string // YYYY-MM-DD

	// ShotLimit: nil = default, 0 = unlimited, positive = limit per guest.
	ShotLimit        *int
	RevealMode       *string // instant | delayed
	RevealDelayHours *int
}

// QRFormat selects the QR image encoding.
type QRFormat string

const (
	QRPNG QRFormat = "png"
	QRSVG QRFormat = "svg"
)
