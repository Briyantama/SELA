package rbac

import (
	"context"
	"fmt"
)

// Repository is the storage the Service depends on.
type Repository interface {
	// HostRoleAndPreferences returns ErrUnknownHost when the host id does not exist.
	HostRoleAndPreferences(ctx context.Context, hostID string) (roleCode string, theme, language *string, err error)
	RoleDefaults(ctx context.Context, roleCode string) (theme, language *string, err error)
	RoleName(ctx context.Context, roleCode, lang, defaultLang string) (string, error)
	GlobalDefaultTheme(ctx context.Context) (string, error)
	DefaultLanguage(ctx context.Context) (string, error)
	ActiveLanguages(ctx context.Context) ([]Language, error)
	IsActiveLanguage(ctx context.Context, code string) (bool, error)
	MenusForRole(ctx context.Context, roleCode, lang, defaultLang string) ([]Menu, error)
	ActionsForRole(ctx context.Context, roleCode, lang, defaultLang string) (map[string][]ActionPermission, []string, error)
	DropdownsForRole(ctx context.Context, roleCode, lang, defaultLang string) (map[string]Dropdown, error)
	HasPermission(ctx context.Context, hostID, resource, action string) (bool, error)
	UpdatePreferences(ctx context.Context, hostID string, theme, language *string) error
}

// Checker answers whether a host, acting as their role, holds a permission. *Service implements it.
type Checker interface {
	Has(ctx context.Context, hostID string, p Permission) (bool, error)
}

// Service resolves a host's RBAC profile: their role's menus, actions and dropdown options, and
// their theme/language preferences.
type Service struct {
	repo Repository
}

// NewService returns the use case backed by the given repository.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

var _ Checker = (*Service)(nil)

// Has reports whether hostID's active role holds the permission. Deny by default: any missing or
// inactive link (an unknown host included) answers false, never an error.
func (s *Service) Has(ctx context.Context, hostID string, p Permission) (bool, error) {
	return s.repo.HasPermission(ctx, hostID, p.Resource, p.Action)
}

// Profile resolves the full /auth/me payload for hostID. langOverride, when non-empty and an
// active language, wins over the host's and role's stored language.
func (s *Service) Profile(ctx context.Context, hostID, langOverride string) (Profile, error) {
	roleCode, hostTheme, hostLang, err := s.repo.HostRoleAndPreferences(ctx, hostID)
	if err != nil {
		return Profile{}, err
	}

	roleTheme, roleLang, err := s.repo.RoleDefaults(ctx, roleCode)
	if err != nil {
		return Profile{}, fmt.Errorf("role defaults for %q: %w", roleCode, err)
	}
	globalTheme, err := s.repo.GlobalDefaultTheme(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("global default theme: %w", err)
	}
	defaultLang, err := s.repo.DefaultLanguage(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("default language: %w", err)
	}
	activeLangs, err := s.repo.ActiveLanguages(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("active languages: %w", err)
	}

	theme, themeSource := resolveTheme(hostTheme, roleTheme, globalTheme)
	lang, langSource := resolveLanguage(langOverride, hostLang, roleLang, defaultLang, activeLangs)

	roleName, err := s.repo.RoleName(ctx, roleCode, lang, defaultLang)
	if err != nil {
		return Profile{}, fmt.Errorf("role name: %w", err)
	}
	menus, err := s.repo.MenusForRole(ctx, roleCode, lang, defaultLang)
	if err != nil {
		return Profile{}, fmt.Errorf("menus: %w", err)
	}
	actions, permissions, err := s.repo.ActionsForRole(ctx, roleCode, lang, defaultLang)
	if err != nil {
		return Profile{}, fmt.Errorf("actions: %w", err)
	}
	dropdowns, err := s.repo.DropdownsForRole(ctx, roleCode, lang, defaultLang)
	if err != nil {
		return Profile{}, fmt.Errorf("dropdowns: %w", err)
	}

	return Profile{
		RoleCode: roleCode,
		RoleName: roleName,
		Preferences: Preferences{
			Theme: theme, ThemeSource: themeSource,
			Language: lang, LanguageSource: langSource,
			SupportedLanguages: activeLangs,
		},
		Menus: menus, Permissions: permissions, Actions: actions, Dropdowns: dropdowns,
	}, nil
}

// resolveTheme picks a host override, then the role default, then the global default.
func resolveTheme(host, role *string, global string) (theme, source string) {
	if host != nil {
		return *host, "user"
	}
	if role != nil {
		return *role, "role"
	}
	return global, "default"
}

// resolveLanguage picks a request override, then the host's preference, then the role default,
// then the global default, skipping any candidate that is not an active language.
func resolveLanguage(override string, host, role *string, global string, active []Language) (lang, source string) {
	isActive := func(code string) bool {
		for _, l := range active {
			if l.Code == code {
				return true
			}
		}
		return false
	}
	candidates := []struct{ code, source string }{{override, "request"}}
	if host != nil {
		candidates = append(candidates, struct{ code, source string }{*host, "user"})
	}
	if role != nil {
		candidates = append(candidates, struct{ code, source string }{*role, "role"})
	}
	candidates = append(candidates, struct{ code, source string }{global, "default"})

	for _, c := range candidates {
		if c.code != "" && isActive(c.code) {
			return c.code, c.source
		}
	}
	return global, "default"
}

// UpdatePreferences validates and applies theme/language overrides, then returns the refreshed
// profile. A nil field leaves that preference unchanged.
func (s *Service) UpdatePreferences(ctx context.Context, hostID string, in PreferencesInput) (Profile, error) {
	if in.Theme != nil && *in.Theme != "light" && *in.Theme != "dark" {
		return Profile{}, &ValidationError{Message: "theme must be light or dark"}
	}
	if in.Language != nil {
		active, err := s.repo.IsActiveLanguage(ctx, *in.Language)
		if err != nil {
			return Profile{}, fmt.Errorf("check language: %w", err)
		}
		if !active {
			return Profile{}, &ValidationError{Message: "unknown language code"}
		}
	}
	if err := s.repo.UpdatePreferences(ctx, hostID, in.Theme, in.Language); err != nil {
		return Profile{}, err
	}
	return s.Profile(ctx, hostID, "")
}
