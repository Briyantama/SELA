package rbac

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Briyantama/SELA/internal/httpx"
	"github.com/Briyantama/SELA/services/auth"
)

const (
	maxBodyBytes = 4 << 10

	msgInternal     = "internal error"
	msgUnauthorized = "authentication required"
	msgForbidden    = "forbidden"
)

// ProfileService is the use-case surface the HTTP adapter depends on. *Service implements it.
type ProfileService interface {
	Profile(ctx context.Context, hostID, langOverride string) (Profile, error)
	UpdatePreferences(ctx context.Context, hostID string, in PreferencesInput) (Profile, error)
	Has(ctx context.Context, hostID string, p Permission) (bool, error)
}

var _ ProfileService = (*Service)(nil)

// HostGuard rejects requests without a valid host session. *auth.HTTPHandler implements it.
type HostGuard interface {
	RequireHost(next http.Handler) http.Handler
}

// HTTPHandler exposes the RBAC profile as JSON endpoints, and RequirePermission as middleware
// other services can wrap their own routes in.
type HTTPHandler struct {
	svc   ProfileService
	guard HostGuard
}

// NewHTTPHandler returns the HTTP adapter for the use case.
func NewHTTPHandler(svc ProfileService, guard HostGuard) *HTTPHandler {
	return &HTTPHandler{svc: svc, guard: guard}
}

// Register mounts the RBAC routes on the mux. Both need a session; updating preferences also needs
// the profile:update permission.
func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/auth/me", h.guard.RequireHost(http.HandlerFunc(h.me)))
	mux.Handle("PATCH /api/v1/me/preferences",
		h.guard.RequireHost(h.RequirePermission("profile:update")(http.HandlerFunc(h.updatePreferences))))
}

// RequirePermission returns middleware that lets a request through only if the authenticated host
// holds the permission. It panics on a malformed code, so a typo fails at start-up, not at request
// time. Exported so other services (e.g. event) can gate their own routes without importing rbac's
// concrete Service type; it must run after a HostGuard so auth.HostID is already in context.
func (h *HTTPHandler) RequirePermission(code string) func(http.Handler) http.Handler {
	perm, err := ParsePermission(code)
	if err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hostID, ok := auth.HostID(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusUnauthorized, msgUnauthorized)
				return
			}
			allowed, err := h.svc.Has(r.Context(), hostID, perm)
			if err != nil {
				slog.Error("permission check failed", "permission", perm.String(), "error", err)
				httpx.WriteError(w, http.StatusInternalServerError, msgInternal)
				return
			}
			if !allowed {
				httpx.WriteError(w, http.StatusForbidden, msgForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type menuJSON struct {
	Code      string     `json:"code"`
	Type      string     `json:"type"`
	Label     string     `json:"label"`
	Tooltip   *string    `json:"tooltip,omitempty"`
	Icon      *string    `json:"icon,omitempty"`
	Path      *string    `json:"path,omitempty"`
	SortOrder int        `json:"sort_order"`
	Layout    layoutJSON `json:"layout"`
	Children  []menuJSON `json:"children"`
}

type layoutJSON struct {
	HideOnMobile   bool `json:"hide_on_mobile"`
	HideOnTablet   bool `json:"hide_on_tablet"`
	MobilePriority *int `json:"mobile_priority,omitempty"`
}

func toMenuJSON(m Menu) menuJSON {
	children := make([]menuJSON, 0, len(m.Children))
	for _, c := range m.Children {
		children = append(children, toMenuJSON(c))
	}
	return menuJSON{
		Code: m.Code, Type: m.Type, Label: m.Label, Tooltip: m.Tooltip, Icon: m.Icon, Path: m.Path,
		SortOrder: m.SortOrder,
		Layout: layoutJSON{
			HideOnMobile: m.Layout.HideOnMobile, HideOnTablet: m.Layout.HideOnTablet,
			MobilePriority: m.Layout.MobilePriority,
		},
		Children: children,
	}
}

type placementJSON struct {
	Desktop string `json:"desktop"`
	Tablet  string `json:"tablet"`
	Mobile  string `json:"mobile"`
}

type actionJSON struct {
	Code           string        `json:"code"`
	Permission     string        `json:"permission"`
	Label          string        `json:"label"`
	Tooltip        *string       `json:"tooltip,omitempty"`
	ConfirmMessage *string       `json:"confirm_message,omitempty"`
	Icon           string        `json:"icon,omitempty"`
	Variant        string        `json:"variant"`
	Destructive    bool          `json:"destructive"`
	SortOrder      int           `json:"sort_order"`
	Placement      placementJSON `json:"placement"`
}

func toActionJSON(a ActionPermission) actionJSON {
	return actionJSON{
		Code: a.Code, Permission: a.Permission, Label: a.Label, Tooltip: a.Tooltip,
		ConfirmMessage: a.ConfirmMessage, Icon: a.Icon, Variant: a.Variant, Destructive: a.Destructive,
		SortOrder: a.SortOrder,
		Placement: placementJSON{Desktop: a.Placement.Desktop, Tablet: a.Placement.Tablet, Mobile: a.Placement.Mobile},
	}
}

type dropdownItemJSON struct {
	Value     string  `json:"value"`
	Label     string  `json:"label"`
	Icon      *string `json:"icon,omitempty"`
	IsDefault bool    `json:"is_default"`
}

type presentationJSON struct {
	Mobile string `json:"mobile"`
}

type dropdownJSON struct {
	Label        string             `json:"label"`
	Placeholder  *string            `json:"placeholder,omitempty"`
	Searchable   bool               `json:"searchable"`
	Presentation presentationJSON   `json:"presentation"`
	Items        []dropdownItemJSON `json:"items"`
}

func toDropdownJSON(d Dropdown) dropdownJSON {
	items := make([]dropdownItemJSON, 0, len(d.Items))
	for _, i := range d.Items {
		items = append(items, dropdownItemJSON{Value: i.Value, Label: i.Label, Icon: i.Icon, IsDefault: i.IsDefault})
	}
	return dropdownJSON{
		Label: d.Label, Placeholder: d.Placeholder, Searchable: d.Searchable,
		Presentation: presentationJSON{Mobile: d.MobilePresentation}, Items: items,
	}
}

type languageJSON struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type sourceJSON struct {
	Theme    string `json:"theme"`
	Language string `json:"language"`
}

type preferencesJSON struct {
	Theme              string         `json:"theme"`
	Language           string         `json:"language"`
	Source             sourceJSON     `json:"source"`
	SupportedLanguages []languageJSON `json:"supported_languages"`
}

type roleJSON struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type profileJSON struct {
	ActiveRole  roleJSON                `json:"active_role"`
	Preferences preferencesJSON         `json:"preferences"`
	Menus       []menuJSON              `json:"menus"`
	Permissions []string                `json:"permissions"`
	Actions     map[string][]actionJSON `json:"actions"`
	Dropdowns   map[string]dropdownJSON `json:"dropdowns"`
}

func toProfileJSON(p Profile) profileJSON {
	menus := make([]menuJSON, 0, len(p.Menus))
	for _, m := range p.Menus {
		menus = append(menus, toMenuJSON(m))
	}

	actions := make(map[string][]actionJSON, len(p.Actions))
	for resource, list := range p.Actions {
		out := make([]actionJSON, 0, len(list))
		for _, a := range list {
			out = append(out, toActionJSON(a))
		}
		actions[resource] = out
	}

	dropdowns := make(map[string]dropdownJSON, len(p.Dropdowns))
	for code, d := range p.Dropdowns {
		dropdowns[code] = toDropdownJSON(d)
	}

	langs := make([]languageJSON, 0, len(p.Preferences.SupportedLanguages))
	for _, l := range p.Preferences.SupportedLanguages {
		langs = append(langs, languageJSON{Code: l.Code, Name: l.Name})
	}

	permissions := p.Permissions
	if permissions == nil {
		permissions = []string{}
	}

	return profileJSON{
		ActiveRole: roleJSON{Code: p.RoleCode, Name: p.RoleName},
		Preferences: preferencesJSON{
			Theme: p.Preferences.Theme, Language: p.Preferences.Language,
			Source:             sourceJSON{Theme: p.Preferences.ThemeSource, Language: p.Preferences.LanguageSource},
			SupportedLanguages: langs,
		},
		Menus: menus, Permissions: permissions, Actions: actions, Dropdowns: dropdowns,
	}
}

func (h *HTTPHandler) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	hostID, ok := auth.HostID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, msgUnauthorized)
		return
	}
	p, err := h.svc.Profile(r.Context(), hostID, r.URL.Query().Get("lang"))
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toProfileJSON(p))
}

func (h *HTTPHandler) updatePreferences(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	hostID, ok := auth.HostID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, msgUnauthorized)
		return
	}
	var body struct {
		Theme    *string `json:"theme"`
		Language *string `json:"language"`
	}
	if !httpx.DecodeJSON(w, r, &body, maxBodyBytes) {
		return
	}
	p, err := h.svc.UpdatePreferences(r.Context(), hostID, PreferencesInput{Theme: body.Theme, Language: body.Language})
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toProfileJSON(p))
}

// writeHTTPError maps a use-case error to a status code and a fixed or validation message.
// Internal details never reach the client.
func writeHTTPError(w http.ResponseWriter, err error) {
	var validation *ValidationError
	switch {
	case errors.As(err, &validation):
		httpx.WriteError(w, http.StatusBadRequest, validation.Message)
	default:
		slog.Error("rbac request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, msgInternal)
	}
}
