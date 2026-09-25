package media

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Briyantama/SELA/internal/httpx"
	"github.com/Briyantama/SELA/services/auth"
)

const (
	guestCookieName = "sela_guest"
	maxBodyBytes    = 4 << 10
	eventsPrefix    = "/api/v1/events/"

	msgNotFound    = "not found"
	msgGuestNeeded = "guest session required"
	msgUnsupported = "this file type is not supported yet"
	msgTooLarge    = "file too large"
	msgCameraFull  = "your camera is full"
	msgNotReceived = "upload not received yet"
	msgUnprocessed = "the file could not be processed"
	msgInternal    = "internal error"
	msgRateLimited = "too many requests, try again later"

	// Fixed-window limits for the public, abuse-prone routes (FR-SEC.9). Uploads are limited per
	// guest session and, as a backstop against session farming, per connection address.
	limitWindow          = 10 * time.Minute
	sessionLimitPerIP    = 20
	uploadLimitPerGuest  = 60
	uploadLimitPerClient = 300

	scopeSession  = "guest-session"
	scopeUpload   = "upload"
	scopeUploadIP = "upload-ip"
)

// Guests is what the HTTP adapter needs from the service; *Service implements it.
type Guests interface {
	StartSession(ctx context.Context, eventID, token string, nickname *string) (StartedSession, error)
	Authenticate(ctx context.Context, eventID, token string) (Guest, error)
	BeginUpload(ctx context.Context, g Guest, contentType string, size int64) (Upload, error)
	CompleteUpload(ctx context.Context, g Guest, mediaID string) (Item, error)
	MyMedia(ctx context.Context, g Guest) ([]Item, error)
	HallOfFame(ctx context.Context, g Guest) ([]Item, error)
	HostMedia(ctx context.Context, hostID, eventID string) ([]Item, error)
}

// HostGuard rejects requests without a valid host session. *auth.HTTPHandler implements it.
type HostGuard interface {
	RequireHost(next http.Handler) http.Handler
}

// Limiter counts a hit against a fixed window; *auth.WindowLimiter implements it.
type Limiter interface {
	Allow(ctx context.Context, scope, subject string, limit int, window time.Duration) (ok bool, retryAfter time.Duration, err error)
}

// HTTPConfig configures the adapter.
type HTTPConfig struct {
	// Limiter rate limits guest-session and upload requests. Nil disables limiting (tests only).
	Limiter Limiter
	// CookieSecure marks the guest cookie Secure. Disable only for plain-HTTP local development.
	CookieSecure bool
}

// HTTPHandler exposes guest sessions, uploads and the galleries (FSD 6).
type HTTPHandler struct {
	svc   Guests
	guard HostGuard
	cfg   HTTPConfig
}

// NewHTTPHandler returns the adapter.
func NewHTTPHandler(svc Guests, guard HostGuard, cfg HTTPConfig) *HTTPHandler {
	return &HTTPHandler{svc: svc, guard: guard, cfg: cfg}
}

// Register mounts the guest routes (behind the guest cookie) and the host gallery (behind RequireHost).
func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.Handle("POST "+eventsPrefix+"{event_id}/guest-session", private(http.HandlerFunc(h.startSession)))
	mux.Handle("POST "+eventsPrefix+"{event_id}/media/uploads", private(h.guest(h.beginUpload)))
	mux.Handle("POST "+eventsPrefix+"{event_id}/media/{media_id}/complete", private(h.guest(h.completeUpload)))
	mux.Handle("GET "+eventsPrefix+"{event_id}/my-media", private(h.guest(h.myMedia)))
	mux.Handle("GET "+eventsPrefix+"{event_id}/hall-of-fame", private(h.guest(h.hallOfFame)))
	mux.Handle("GET "+eventsPrefix+"{event_id}/media", private(h.guard.RequireHost(http.HandlerFunc(h.hostMedia))))
}

// private marks every response, errors included, as uncacheable and unindexable: galleries and media
// are never public (FSD 8.5, 8.11, FR-SEC.10), and a URL must not leak through the Referer header.
func private(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Robots-Tag", "noindex, nofollow")
		header.Set("Cache-Control", "no-store")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// guest resolves the guest cookie for the event in the path, or answers 401.
func (h *HTTPHandler) guest(next func(http.ResponseWriter, *http.Request, Guest)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g, err := h.svc.Authenticate(r.Context(), r.PathValue("event_id"), guestToken(r))
		if err != nil {
			writeError(w, err)
			return
		}
		next(w, r, g)
	})
}

// allow counts one hit and answers 429 (with Retry-After) or 500 itself when the request must stop.
func (h *HTTPHandler) allow(w http.ResponseWriter, r *http.Request, scope, subject string, limit int) bool {
	if h.cfg.Limiter == nil {
		return true
	}
	ok, retry, err := h.cfg.Limiter.Allow(r.Context(), scope, subject, limit, limitWindow)
	if err != nil {
		slog.Error("rate limiter failed", "scope", scope, "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, msgInternal)
		return false
	}
	if !ok {
		w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(retry.Seconds())), 1)))
		httpx.WriteError(w, http.StatusTooManyRequests, msgRateLimited)
		return false
	}
	return true
}

func guestToken(r *http.Request) string {
	if c, err := r.Cookie(guestCookieName); err == nil {
		return c.Value
	}
	return ""
}

type sessionJSON struct {
	SessionID        string `json:"session_id"`
	EventID          string `json:"event_id"`
	Resumed          bool   `json:"resumed"`
	ShotLimit        *int   `json:"shot_limit"`
	ShotsRemaining   *int   `json:"shots_remaining"`
	InvolvesMinors   bool   `json:"involves_minors"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

func (h *HTTPHandler) startSession(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, scopeSession, auth.ClientAddr(r), sessionLimitPerIP) {
		return
	}
	var body struct {
		Nickname *string `json:"nickname"`
	}
	if r.ContentLength != 0 && !httpx.DecodeJSON(w, r, &body, maxBodyBytes) {
		return
	}
	started, err := h.svc.StartSession(r.Context(), r.PathValue("event_id"), guestToken(r), body.Nickname)
	if err != nil {
		writeError(w, err)
		return
	}

	// The token travels only in the HttpOnly cookie, scoped to this event's API paths (FSD 8.6).
	seconds := int(started.ExpiresIn / time.Second)
	http.SetCookie(w, &http.Cookie{
		Name:     guestCookieName,
		Value:    started.Token,
		Path:     eventsPrefix + started.Guest.EventID,
		MaxAge:   seconds,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	status := http.StatusCreated
	if started.Resumed {
		status = http.StatusOK
	}
	httpx.WriteSuccess(w, status, sessionJSON{
		SessionID: started.Guest.SessionID, EventID: started.Guest.EventID, Resumed: started.Resumed,
		ShotLimit: started.ShotLimit, ShotsRemaining: started.ShotsRemaining,
		InvolvesMinors: started.InvolvesMinors, ExpiresInSeconds: seconds,
	})
}

type uploadRequestJSON struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type uploadJSON struct {
	MediaID        string            `json:"media_id"`
	Upload         uploadRequestJSON `json:"upload"`
	ShotsRemaining *int              `json:"shots_remaining"`
}

func (h *HTTPHandler) beginUpload(w http.ResponseWriter, r *http.Request, g Guest) {
	if !h.allow(w, r, scopeUpload, g.SessionID, uploadLimitPerGuest) ||
		!h.allow(w, r, scopeUploadIP, auth.ClientAddr(r), uploadLimitPerClient) {
		return
	}
	var body struct {
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if !httpx.DecodeJSON(w, r, &body, maxBodyBytes) {
		return
	}
	up, err := h.svc.BeginUpload(r.Context(), g, body.ContentType, body.SizeBytes)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusCreated, uploadJSON{
		MediaID: up.MediaID,
		Upload: uploadRequestJSON{
			Method: up.Request.Method, URL: up.Request.URL, Headers: up.Request.Headers, ExpiresAt: up.Request.ExpiresAt.UTC(),
		},
		ShotsRemaining: up.ShotsRemaining,
	})
}

func (h *HTTPHandler) completeUpload(w http.ResponseWriter, r *http.Request, g Guest) {
	item, err := h.svc.CompleteUpload(r.Context(), g, r.PathValue("media_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toItemJSON(item))
}

func (h *HTTPHandler) myMedia(w http.ResponseWriter, r *http.Request, g Guest) {
	items, err := h.svc.MyMedia(r.Context(), g)
	writeItems(w, items, err)
}

func (h *HTTPHandler) hallOfFame(w http.ResponseWriter, r *http.Request, g Guest) {
	items, err := h.svc.HallOfFame(r.Context(), g)
	writeItems(w, items, err)
}

func (h *HTTPHandler) hostMedia(w http.ResponseWriter, r *http.Request) {
	hostID, _ := auth.HostID(r.Context()) // RequireHost guarantees it
	items, err := h.svc.HostMedia(r.Context(), hostID, r.PathValue("event_id"))
	writeItems(w, items, err)
}

// itemJSON never carries the storage key or the uploader's session id.
type itemJSON struct {
	MediaID         string     `json:"media_id"`
	Kind            Kind       `json:"kind"`
	ContentType     string     `json:"content_type"`
	ProcessingState string     `json:"processing_state"`
	Status          string     `json:"status"`
	SizeBytes       *int64     `json:"size_bytes"`
	HallOfFameRank  *int       `json:"hall_of_fame_rank"`
	UploadedAt      time.Time  `json:"uploaded_at"`
	ReadyAt         *time.Time `json:"ready_at"`
	URL             string     `json:"url,omitempty"`
}

func toItemJSON(item Item) itemJSON {
	return itemJSON{
		MediaID: item.ID, Kind: item.Kind, ContentType: item.ContentType,
		ProcessingState: string(item.Processing), Status: string(item.Status), SizeBytes: item.SizeBytes,
		HallOfFameRank: item.HallOfFameRank, UploadedAt: item.UploadedAt.UTC(), ReadyAt: item.ReadyAt, URL: item.URL,
	}
}

func writeItems(w http.ResponseWriter, items []Item, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]itemJSON, 0, len(items))
	for _, item := range items {
		out = append(out, toItemJSON(item))
	}
	httpx.WriteSuccess(w, http.StatusOK, map[string][]itemJSON{"items": out})
}

// writeError maps service errors to fixed messages; causes are logged, never returned.
func writeError(w http.ResponseWriter, err error) {
	var verr *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, msgNotFound)
	case errors.Is(err, ErrUnauthorized):
		httpx.WriteError(w, http.StatusUnauthorized, msgGuestNeeded)
	case errors.Is(err, ErrUnsupportedType):
		httpx.WriteError(w, http.StatusUnsupportedMediaType, msgUnsupported)
	case errors.Is(err, ErrTooLarge):
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, msgTooLarge)
	case errors.Is(err, ErrQuotaExhausted):
		httpx.WriteError(w, http.StatusTooManyRequests, msgCameraFull)
	case errors.Is(err, ErrUploadMissing):
		httpx.WriteError(w, http.StatusConflict, msgNotReceived)
	case errors.Is(err, ErrRejected):
		slog.Info("upload rejected", "err", err)
		httpx.WriteError(w, http.StatusUnprocessableEntity, msgUnprocessed)
	case errors.As(err, &verr):
		httpx.WriteError(w, http.StatusBadRequest, verr.Error())
	default:
		slog.Error("media request failed", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, msgInternal)
	}
}
