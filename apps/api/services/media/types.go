// Package media owns guest and host uploads: guest sessions, the media lifecycle (pending, ready, failed),
// moderation status, and Hall of Fame flags (FSD 4.3-4.5, 4.7, 5). This file holds the domain types that the
// schema in migration 0006 stores; the use cases come in later tasks.
package media

import (
	"errors"
	"strings"
	"time"
)

// Sentinel errors. Adapters map them to transport status codes.
var (
	// ErrNotFound covers a missing item and an item outside the caller's scope, so callers cannot tell them apart.
	ErrNotFound = errors.New("media not found")
	// ErrUnsupportedType is returned for a content type outside the accepted formats (FR-04.1).
	ErrUnsupportedType = errors.New("unsupported media type")
)

// Kind is what a media item holds.
type Kind string

// The kinds the schema accepts (media_kind_valid).
const (
	KindPhoto Kind = "photo"
	KindVideo Kind = "video"
)

// contentTypes lists the accepted formats and their kind: FR-04.1 (JPEG/PNG/HEIC, MP4/MOV) plus WebP,
// which the camera pipeline outputs (FSD 7). It mirrors media_content_type_valid in migration 0006.
var contentTypes = map[string]Kind{
	"image/jpeg":      KindPhoto,
	"image/png":       KindPhoto,
	"image/heic":      KindPhoto,
	"image/webp":      KindPhoto,
	"video/mp4":       KindVideo,
	"video/quicktime": KindVideo,
}

// KindOf returns the kind for an accepted content type, ignoring case and parameters ("image/JPEG; q=1").
// The declared type is only a first filter: the stored bytes are checked by magic number (FR-SEC.3).
func KindOf(contentType string) (Kind, string, error) {
	mediaType, _, _ := strings.Cut(contentType, ";")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	kind, ok := contentTypes[mediaType]
	if !ok {
		return "", "", ErrUnsupportedType
	}
	return kind, mediaType, nil
}

// ProcessingState tracks an upload from signed-URL issue to a servable item.
type ProcessingState string

// Processing states (media_processing_state_valid).
const (
	// ProcessingPending: the upload URL was issued; the object may not exist yet.
	ProcessingPending ProcessingState = "pending"
	// ProcessingReady: bytes verified, EXIF/GPS stripped (FR-SEC.2, FR-SEC.3); the item may be served.
	ProcessingReady ProcessingState = "ready"
	// ProcessingFailed: the object never arrived or failed verification; its shot is returned to the quota.
	ProcessingFailed ProcessingState = "failed"
)

// Status is the moderation status (FSD 5: aktif/disembunyikan/dihapus).
type Status string

// Moderation statuses (media_status_valid).
const (
	StatusActive  Status = "active"
	StatusHidden  Status = "hidden"
	StatusDeleted Status = "deleted"
)

// Media is one stored item. ObjectKey is a storage key, never a URL: callers get short-lived signed URLs.
type Media struct {
	ID        string
	EventID   string
	SessionID *string // nil = uploaded by the host

	Kind        Kind
	ContentType string
	ObjectKey   string
	SizeBytes   *int64 // nil until processed

	Processing ProcessingState
	Status     Status

	HallOfFame     bool
	HallOfFameRank *int // set exactly when HallOfFame is true

	ReactionCount int
	UploadedAt    time.Time
	ReadyAt       *time.Time
	DeletedAt     *time.Time
}

// Servable reports whether the item may be shown to anyone other than its uploader and the host.
func (m Media) Servable() bool {
	return m.Processing == ProcessingReady && m.Status == StatusActive
}

// GuestSession is an anonymous guest on one device for one event (FSD 4.2, FR-02.2). It carries no
// device fingerprint (data minimisation, FSD 8.11); the device is known only by its session cookie.
type GuestSession struct {
	ID         string
	EventID    string
	Nickname   *string // optional, at most 40 characters
	CreatedAt  time.Time
	LastSeenAt time.Time
}
