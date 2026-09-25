package auth

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Briyantama/SELA/internal/httpx"
)

const (
	requestPath       = "/api/v1/auth/otp/request"
	verifyPath        = "/api/v1/auth/otp/verify"
	sessionCookieName = "sela_session"
	maxBodyBytes      = 4 << 10

	msgInternal     = "internal error"
	msgInvalidEmail = "invalid email address"
	msgInvalidCode  = "invalid or expired code"
	msgRateLimited  = "too many requests"
	msgLocked       = "too many failed attempts"
	msgDelivery     = "could not send the code"
	msgUnauthorized = "authentication required"
)

// HTTPConfig configures the HTTP adapter.
type HTTPConfig struct {
	// CookieSecure marks the session cookie Secure. Disable only for plain-HTTP local development.
	CookieSecure bool
}

// HTTPHandler exposes the sign-in flow as JSON endpoints and a session-checking middleware.
type HTTPHandler struct {
	flow Flow
	cfg  HTTPConfig
}

// NewHTTPHandler returns the HTTP adapter for the flow.
func NewHTTPHandler(flow Flow, cfg HTTPConfig) *HTTPHandler {
	return &HTTPHandler{flow: flow, cfg: cfg}
}

// Register mounts POST /api/v1/auth/otp/request and POST /api/v1/auth/otp/verify on the mux.
func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+requestPath, h.requestOTP)
	mux.HandleFunc("POST "+verifyPath, h.verifyOTP)
}

type hostContextKey struct{}

// HostID returns the authenticated host placed in the context by RequireHost.
func HostID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(hostContextKey{}).(string)
	return id, ok && id != ""
}

// RequireHost rejects requests without a valid session cookie and otherwise passes the host id
// to next through the request context (see HostID).
func (h *HTTPHandler) RequireHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			httpx.WriteError(w, http.StatusUnauthorized, msgUnauthorized)
			return
		}
		hostID, err := h.flow.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			h.writeError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), hostContextKey{}, hostID)))
	})
}

func (h *HTTPHandler) requestOTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	var body struct {
		Email string `json:"email"`
	}
	if !httpx.DecodeJSON(w, r, &body, maxBodyBytes) {
		return
	}

	res, err := h.flow.RequestOTP(r.Context(), body.Email, ClientAddr(r))
	if err != nil {
		h.writeError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, map[string]int{"expires_in_seconds": seconds(res.ExpiresIn)})
}

func (h *HTTPHandler) verifyOTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !httpx.DecodeJSON(w, r, &body, maxBodyBytes) {
		return
	}

	session, err := h.flow.VerifyOTP(r.Context(), body.Email, body.Code)
	if err != nil {
		h.writeError(w, err)
		return
	}

	// The token travels only in the HttpOnly cookie, never in the response body.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		MaxAge:   seconds(session.ExpiresIn),
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	httpx.WriteSuccess(w, http.StatusOK, map[string]any{
		"host_id":            session.HostID,
		"is_new_host":        session.IsNewHost,
		"expires_in_seconds": seconds(session.ExpiresIn),
	})
}

// writeError maps a flow error to a status code and a fixed message. Internal details never reach the client.
func (h *HTTPHandler) writeError(w http.ResponseWriter, err error) {
	var retry *RetryAfterError
	switch {
	case errors.Is(err, ErrInvalidEmail):
		httpx.WriteError(w, http.StatusBadRequest, msgInvalidEmail)
	case errors.Is(err, ErrInvalidCode):
		httpx.WriteError(w, http.StatusUnauthorized, msgInvalidCode)
	case errors.Is(err, ErrUnauthenticated):
		httpx.WriteError(w, http.StatusUnauthorized, msgUnauthorized)
	case errors.Is(err, ErrRateLimited), errors.Is(err, ErrLocked):
		if errors.As(err, &retry) {
			w.Header().Set("Retry-After", strconv.Itoa(max(seconds(retry.After), 1)))
		}
		message := msgRateLimited
		if errors.Is(err, ErrLocked) {
			message = msgLocked
		}
		httpx.WriteError(w, http.StatusTooManyRequests, message)
	case errors.Is(err, ErrDelivery):
		slog.Warn("otp delivery failed", "error", err)
		httpx.WriteError(w, http.StatusBadGateway, msgDelivery)
	default:
		slog.Error("auth request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, msgInternal)
	}
}

// ClientAddr identifies the caller by its connection address. Forwarded headers are deliberately
// ignored: any client can spoof them to dodge rate limits. Behind a trusted proxy, configure the
// proxy to set the connection address instead.
func ClientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func seconds(d time.Duration) int {
	return int(math.Ceil(d.Seconds()))
}
