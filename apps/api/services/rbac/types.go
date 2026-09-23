// Package rbac serves the host web app's navigation, permitted actions and theme/language
// preferences as data: what a host's role grants (menus, dropdown options, actions/permissions)
// and how theme/language resolve through a host -> role -> global default fallback chain.
package rbac

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrUnknownHost means the host id from the session does not exist. Callers treat it as an
// internal error: RequireHost already authenticated the session, so a missing host row is a bug,
// not a caller mistake.
var ErrUnknownHost = errors.New("unknown host")

// ValidationError is a problem with the caller's input. Its message is safe to show to the caller.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// MenuLayout carries the responsive hints a client uses to place a menu item; it never decides
// visibility itself (RequireHost + the role grant already did that).
type MenuLayout struct {
	HideOnMobile   bool
	HideOnTablet   bool
	MobilePriority *int // bottom-nav slot 1-5; nil = drawer only
}

// Menu is one node of the role's navigation tree, already filtered and translated.
type Menu struct {
	Code      string
	Type      string // link | group | divider
	Label     string
	Tooltip   *string
	Icon      *string
	Path      *string
	SortOrder int
	Layout    MenuLayout
	Children  []Menu
}

// Placement is where an action is drawn at each breakpoint.
type Placement struct {
	Desktop string
	Tablet  string
	Mobile  string
}

// ActionPermission is one action a role may perform on a resource, with its UI presentation.
type ActionPermission struct {
	Code           string
	Permission     string // "resource:action"
	Label          string
	Tooltip        *string
	ConfirmMessage *string
	Icon           string
	Variant        string
	Destructive    bool
	SortOrder      int
	Placement      Placement
}

// DropdownItem is one option in a Dropdown.
type DropdownItem struct {
	Value     string
	Label     string
	Icon      *string
	IsDefault bool
	SortOrder int
}

// Dropdown is a named, translated list of options a role may see.
type Dropdown struct {
	Label              string
	Placeholder        *string
	Searchable         bool
	MobilePresentation string
	Items              []DropdownItem
}

// Language is one entry in the language picker.
type Language struct {
	Code string
	Name string
}

// Preferences is the host's resolved theme and language, with where each value came from.
type Preferences struct {
	Theme       string // light | dark
	ThemeSource string // user | role | default

	Language       string
	LanguageSource string // user | role | default

	SupportedLanguages []Language
}

// Profile is the full payload behind GET /api/v1/auth/me.
type Profile struct {
	RoleCode string
	RoleName string

	Preferences Preferences
	Menus       []Menu
	Permissions []string
	Actions     map[string][]ActionPermission // keyed by resource code
	Dropdowns   map[string]Dropdown           // keyed by dropdown code
}

// PreferencesInput is the body of PATCH /api/v1/me/preferences. A nil field leaves that
// preference unchanged; there is no way to clear an override back to "inherited" yet.
type PreferencesInput struct {
	Theme    *string
	Language *string
}

// Permission is a resource and an action, written "resource:action" (e.g. "events:create").
type Permission struct {
	Resource string
	Action   string
}

func (p Permission) String() string { return p.Resource + ":" + p.Action }

var permissionCodePart = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ParsePermission validates the "resource:action" form used by RequirePermission and the seed data.
func ParsePermission(code string) (Permission, error) {
	resource, action, ok := strings.Cut(code, ":")
	if !ok || !permissionCodePart.MatchString(resource) || !permissionCodePart.MatchString(action) {
		return Permission{}, fmt.Errorf("invalid permission code %q, want resource:action", code)
	}
	return Permission{Resource: resource, Action: action}, nil
}
