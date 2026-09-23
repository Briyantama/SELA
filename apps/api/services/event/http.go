package event

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Briyantama/SELA/internal/httpx"
	"github.com/Briyantama/SELA/services/auth"
)

const (
	maxBodyBytes = 4 << 10

	msgInternal     = "internal error"
	msgNotFound     = "event not found"
	msgUnauthorized = "authentication required"
)

// Events is the use-case surface the transport adapters depend on. *Service implements it; tests
// substitute stubs to force specific outcomes.
type Events interface {
	ListCategories(ctx context.Context) ([]Category, error)
	CreateEvent(ctx context.Context, hostID string, in CreateInput) (Event, error)
	GetEvent(ctx context.Context, hostID, eventID string) (Event, error)
	QRCode(ctx context.Context, hostID, eventID string, format QRFormat) ([]byte, string, error)
	ResolveShortCode(ctx context.Context, code string) (PublicEvent, error)
}

var _ Events = (*Service)(nil)

// HostGuard rejects requests without a valid host session. *auth.HTTPHandler implements it.
type HostGuard interface {
	RequireHost(next http.Handler) http.Handler
}

// PermissionGuard rejects requests whose host lacks a permission. *rbac.HTTPHandler implements it.
type PermissionGuard interface {
	RequirePermission(code string) func(http.Handler) http.Handler
}

// HTTPHandler exposes the event use cases as JSON endpoints. Category presets and the short-link
// resolver are public; every route that creates or reads a specific event as its owner sits behind
// the HostGuard. Creating an event additionally needs the events:create permission.
type HTTPHandler struct {
	events Events
	guard  HostGuard
	perms  PermissionGuard
}

// NewHTTPHandler returns the HTTP adapter for the use cases.
func NewHTTPHandler(events Events, guard HostGuard, perms PermissionGuard) *HTTPHandler {
	return &HTTPHandler{events: events, guard: guard, perms: perms}
}

// Register mounts the event routes on the mux.
func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/event-categories", h.listCategories)
	mux.HandleFunc("GET /e/{short_code}", h.resolveShortCode)
	mux.Handle("POST /api/v1/events",
		h.guard.RequireHost(h.perms.RequirePermission("events:create")(http.HandlerFunc(h.createEvent))))
	mux.Handle("GET /api/v1/events/{id}", h.guard.RequireHost(http.HandlerFunc(h.getEvent)))
	mux.Handle("GET /api/v1/events/{id}/qr.png", h.guard.RequireHost(h.qr(QRPNG)))
	mux.Handle("GET /api/v1/events/{id}/qr.svg", h.guard.RequireHost(h.qr(QRSVG)))
}

type categoryJSON struct {
	Code                      string `json:"code"`
	Name                      string `json:"name"`
	ThemeKey                  string `json:"theme_key"`
	ShotLimitDefault          string `json:"shot_limit_default"`
	DefaultShotLimit          *int   `json:"default_shot_limit"`
	RevealDefault             string `json:"reveal_default"`
	DefaultRevealDelayHours   *int   `json:"default_reveal_delay_hours"`
	EffectiveShotLimit        *int   `json:"effective_shot_limit"`
	EffectiveRevealMode       string `json:"effective_reveal_mode"`
	EffectiveRevealDelayHours *int   `json:"effective_reveal_delay_hours"`
}

// eventJSON is the owner's view of an event. It has no host id and no access token by construction.
type eventJSON struct {
	EventID      string  `json:"event_id"`
	CategoryCode string  `json:"category_code"`
	Name         string  `json:"name"`
	EventDate    string  `json:"event_date"`
	Timezone     string  `json:"timezone"`
	Status       string  `json:"status"`
	ShortCode    string  `json:"short_code"`
	ShortLink    string  `json:"short_link"`
	QRPNGURL     string  `json:"qr_png_url"`
	QRSVGURL     string  `json:"qr_svg_url"`
	ShotLimit    *int    `json:"shot_limit"`
	RevealMode   string  `json:"reveal_mode"`
	RevealAt     *string `json:"reveal_at"`
	Package      string  `json:"package"`
	CreatedAt    string  `json:"created_at"`
}

// publicEventJSON is what a guest sees behind a short link. It mirrors PublicEvent, which itself
// has no owner, billing or storage fields.
type publicEventJSON struct {
	EventID      string  `json:"event_id"`
	ShortCode    string  `json:"short_code"`
	Name         string  `json:"name"`
	EventDate    string  `json:"event_date"`
	Timezone     string  `json:"timezone"`
	CategoryCode string  `json:"category_code"`
	ThemeKey     string  `json:"theme_key"`
	Status       string  `json:"status"`
	ShotLimit    *int    `json:"shot_limit"`
	RevealMode   string  `json:"reveal_mode"`
	RevealAt     *string `json:"reveal_at"`
}

func toPublicEventJSON(e PublicEvent) publicEventJSON {
	out := publicEventJSON{
		EventID: e.EventID, ShortCode: e.ShortCode, Name: e.Name, EventDate: e.EventDate,
		Timezone: e.Timezone, CategoryCode: e.CategoryCode, ThemeKey: e.ThemeKey, Status: e.Status,
		ShotLimit: e.ShotLimit, RevealMode: e.RevealMode,
	}
	if e.RevealAt != nil {
		at := e.RevealAt.UTC().Format(time.RFC3339)
		out.RevealAt = &at
	}
	return out
}

// toCategoryJSON is a plain conversion on purpose: categoryJSON mirrors Category field for field,
// so adding a field to Category without deciding how it is exposed stops the build.
func toCategoryJSON(c Category) categoryJSON {
	return categoryJSON(c)
}

func toEventJSON(e Event) eventJSON {
	out := eventJSON{
		EventID: e.ID, CategoryCode: e.CategoryCode, Name: e.Name, EventDate: e.EventDate,
		Timezone: e.Timezone, Status: e.Status, ShortCode: e.ShortCode, ShortLink: e.ShortLink,
		QRPNGURL: e.QRPNGURL, QRSVGURL: e.QRSVGURL, ShotLimit: e.ShotLimit, RevealMode: e.RevealMode,
		Package: e.Package, CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
	}
	if e.RevealAt != nil {
		at := e.RevealAt.UTC().Format(time.RFC3339)
		out.RevealAt = &at
	}
	return out
}

func (h *HTTPHandler) listCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.events.ListCategories(r.Context())
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	out := make([]categoryJSON, 0, len(cats))
	for _, c := range cats {
		out = append(out, toCategoryJSON(c))
	}
	httpx.WriteSuccess(w, http.StatusOK, map[string]any{"categories": out})
}

func (h *HTTPHandler) createEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	var body struct {
		CategoryCode     string  `json:"category_code"`
		Name             string  `json:"name"`
		EventDate        string  `json:"event_date"`
		ShotLimit        *int    `json:"shot_limit"`
		RevealMode       *string `json:"reveal_mode"`
		RevealDelayHours *int    `json:"reveal_delay_hours"`
	}
	if !httpx.DecodeJSON(w, r, &body, maxBodyBytes) {
		return
	}
	hostID, ok := auth.HostID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, msgUnauthorized)
		return
	}

	ev, err := h.events.CreateEvent(r.Context(), hostID, CreateInput{
		CategoryCode: body.CategoryCode, Name: body.Name, EventDate: body.EventDate,
		ShotLimit: body.ShotLimit, RevealMode: body.RevealMode, RevealDelayHours: body.RevealDelayHours,
	})
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/events/"+ev.ID)
	httpx.WriteSuccess(w, http.StatusCreated, toEventJSON(ev))
}

func (h *HTTPHandler) getEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	hostID, ok := auth.HostID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, msgUnauthorized)
		return
	}
	ev, err := h.events.GetEvent(r.Context(), hostID, r.PathValue("id"))
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toEventJSON(ev))
}

// resolveShortCode is public: the code in the QR is the only credential. Every failure to find a
// usable event answers with the same 404, so a caller cannot tell a missing code from an expired one.
func (h *HTTPHandler) resolveShortCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	ev, err := h.events.ResolveShortCode(r.Context(), r.PathValue("short_code"))
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toPublicEventJSON(ev))
}

func (h *HTTPHandler) qr(format QRFormat) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostID, ok := auth.HostID(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, msgUnauthorized)
			return
		}
		data, contentType, err := h.events.QRCode(r.Context(), hostID, r.PathValue("id"), format)
		if err != nil {
			writeHTTPError(w, err)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Robots-Tag", "noindex")
		if _, err := w.Write(data); err != nil {
			slog.Warn("write QR response", "error", err)
		}
	})
}

// writeHTTPError maps a use-case error to a status code and a fixed or validation message.
// Internal details never reach the client.
func writeHTTPError(w http.ResponseWriter, err error) {
	var validation *ValidationError
	switch {
	case errors.As(err, &validation):
		httpx.WriteError(w, http.StatusBadRequest, validation.Message)
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrUnsupportedFormat):
		httpx.WriteError(w, http.StatusNotFound, msgNotFound)
	default:
		slog.Error("event request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, msgInternal)
	}
}
