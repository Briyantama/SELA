package rbac_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Briyantama/SELA/internal/db"
	"github.com/Briyantama/SELA/internal/testdb"
	"github.com/Briyantama/SELA/services/rbac"
)

type fixture struct {
	conn *sql.DB
	repo *rbac.PostgresRepository
	svc  *rbac.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	conn := testdb.New(t)
	if err := db.Up(context.Background(), conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := rbac.NewPostgresRepository(conn)
	return &fixture{conn: conn, repo: repo, svc: rbac.NewService(repo)}
}

func (f *fixture) host(t *testing.T, email string) string {
	t.Helper()
	var id string
	if err := f.conn.QueryRow(`INSERT INTO hosts (email) VALUES ($1) RETURNING host_id`, email).Scan(&id); err != nil {
		t.Fatalf("insert host: %v", err)
	}
	return id
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *fixture) scanRow(t *testing.T, dest *string, query string, args ...any) {
	t.Helper()
	if err := f.conn.QueryRow(query, args...).Scan(dest); err != nil {
		t.Fatalf("scanRow %q: %v", query, err)
	}
}

func TestProfile_defaultsToTheGlobalThemeAndLanguageWhenNeitherHostNorRoleSetOne(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "alice@example.test")

	// Act
	p, err := f.svc.Profile(context.Background(), host, "")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	if p.Preferences.Theme != "light" || p.Preferences.ThemeSource != "default" {
		t.Errorf("theme = %q/%q, want light/default", p.Preferences.Theme, p.Preferences.ThemeSource)
	}
	if p.Preferences.Language != "id" || p.Preferences.LanguageSource != "default" {
		t.Errorf("language = %q/%q, want id/default", p.Preferences.Language, p.Preferences.LanguageSource)
	}
	if p.RoleCode != "host" || p.RoleName != "Host" {
		t.Errorf("role = %q/%q, want host/Host", p.RoleCode, p.RoleName)
	}
	if len(p.Preferences.SupportedLanguages) != 2 {
		t.Errorf("supported languages = %+v, want 2", p.Preferences.SupportedLanguages)
	}
}

func TestProfile_hostPreferenceWinsOverTheRoleDefault(t *testing.T) {
	// Arrange
	f := newFixture(t)
	f.exec(t, `UPDATE roles SET default_theme_mode = 'dark', default_language_code = 'en' WHERE code = 'host'`)
	host := f.host(t, "bob@example.test")
	f.exec(t, `UPDATE hosts SET theme_mode = 'light', language_code = 'id' WHERE host_id = $1`, host)

	// Act
	p, err := f.svc.Profile(context.Background(), host, "")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	if p.Preferences.Theme != "light" || p.Preferences.ThemeSource != "user" {
		t.Errorf("theme = %q/%q, want light/user", p.Preferences.Theme, p.Preferences.ThemeSource)
	}
	if p.Preferences.Language != "id" || p.Preferences.LanguageSource != "user" {
		t.Errorf("language = %q/%q, want id/user", p.Preferences.Language, p.Preferences.LanguageSource)
	}
}

func TestProfile_roleDefaultWinsWhenTheHostHasNoPreference(t *testing.T) {
	// Arrange
	f := newFixture(t)
	f.exec(t, `UPDATE roles SET default_theme_mode = 'dark', default_language_code = 'en' WHERE code = 'host'`)
	host := f.host(t, "carol@example.test")

	// Act
	p, err := f.svc.Profile(context.Background(), host, "")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	if p.Preferences.Theme != "dark" || p.Preferences.ThemeSource != "role" {
		t.Errorf("theme = %q/%q, want dark/role", p.Preferences.Theme, p.Preferences.ThemeSource)
	}
	if p.Preferences.Language != "en" || p.Preferences.LanguageSource != "role" {
		t.Errorf("language = %q/%q, want en/role", p.Preferences.Language, p.Preferences.LanguageSource)
	}
}

func TestProfile_languageOverrideBeatsEveryStoredPreference(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "dina@example.test")
	f.exec(t, `UPDATE hosts SET language_code = 'id' WHERE host_id = $1`, host)

	// Act
	p, err := f.svc.Profile(context.Background(), host, "en")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	if p.Preferences.Language != "en" || p.Preferences.LanguageSource != "request" {
		t.Errorf("language = %q/%q, want en/request", p.Preferences.Language, p.Preferences.LanguageSource)
	}
	if p.Menus[0].Label != "Create Event" {
		t.Errorf("first menu label = %q, want the English translation", p.Menus[0].Label)
	}
}

func TestProfile_anInactiveOverrideLanguageIsIgnored(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "eko@example.test")

	// Act
	p, err := f.svc.Profile(context.Background(), host, "fr")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	if p.Preferences.Language != "id" || p.Preferences.LanguageSource != "default" {
		t.Errorf("language = %q/%q, want id/default", p.Preferences.Language, p.Preferences.LanguageSource)
	}
}

func TestProfile_unknownHostFails(t *testing.T) {
	// Arrange
	f := newFixture(t)

	// Act
	_, err := f.svc.Profile(context.Background(), "00000000-0000-4000-8000-000000000000", "")

	// Assert
	if !errors.Is(err, rbac.ErrUnknownHost) {
		t.Errorf("err = %v, want ErrUnknownHost", err)
	}
}

func TestProfile_seededMenusAreTranslatedAndInSortOrder(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "fajar@example.test")

	// Act
	p, err := f.svc.Profile(context.Background(), host, "")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	if len(p.Menus) != 2 {
		t.Fatalf("menus = %+v, want 2", p.Menus)
	}
	if p.Menus[0].Code != "events_new" || p.Menus[0].Label != "Buat Acara Baru" || *p.Menus[0].Path != "/events/new" {
		t.Errorf("menus[0] = %+v", p.Menus[0])
	}
	if p.Menus[0].Layout.MobilePriority == nil || *p.Menus[0].Layout.MobilePriority != 1 {
		t.Errorf("menus[0].Layout.MobilePriority = %v, want 1", p.Menus[0].Layout.MobilePriority)
	}
	if p.Menus[1].Code != "settings" {
		t.Errorf("menus[1].Code = %q, want settings", p.Menus[1].Code)
	}
}

func TestProfile_aGrantedChildBringsItsGroupAndAnEmptyGroupIsPruned(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "gita@example.test")
	var groupID, childID, emptyGroupID string
	f.scanRow(t, &groupID, `INSERT INTO menus (code, menu_type, sort_order) VALUES ('grp', 'group', 5) RETURNING menu_id`)
	f.scanRow(t, &childID, `INSERT INTO menus (code, parent_id, menu_type, path, sort_order) VALUES ('grp_child', $1, 'link', '/x', 1) RETURNING menu_id`, groupID)
	f.scanRow(t, &emptyGroupID, `INSERT INTO menus (code, menu_type, sort_order) VALUES ('empty_grp', 'group', 6) RETURNING menu_id`)
	f.exec(t, `INSERT INTO menu_translations (menu_id, language_code, label) VALUES ($1, 'id', 'Grup'), ($2, 'id', 'Anak')`, groupID, childID)
	f.exec(t, `INSERT INTO role_menus (role_id, menu_id) SELECT role_id, $1 FROM roles WHERE code = 'host'`, childID)
	f.exec(t, `INSERT INTO role_menus (role_id, menu_id) SELECT role_id, $1 FROM roles WHERE code = 'host'`, emptyGroupID)

	// Act
	p, err := f.svc.Profile(context.Background(), host, "")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	var group *rbac.Menu
	for i := range p.Menus {
		if p.Menus[i].Code == "grp" {
			group = &p.Menus[i]
		}
		if p.Menus[i].Code == "empty_grp" {
			t.Error("an empty group was not pruned")
		}
	}
	if group == nil {
		t.Fatal("the granted child's ancestor group is missing")
	}
	if len(group.Children) != 1 || group.Children[0].Code != "grp_child" {
		t.Errorf("group.Children = %+v", group.Children)
	}
}

func TestProfile_menuLabelFallsBackToTheDefaultLanguageThenTheCode(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "hana@example.test")
	var id string
	f.scanRow(t, &id, `INSERT INTO menus (code, menu_type, path, sort_order) VALUES ('extra', 'link', '/extra', 3) RETURNING menu_id`)
	f.exec(t, `INSERT INTO role_menus (role_id, menu_id) SELECT role_id, $1 FROM roles WHERE code = 'host'`, id)
	f.exec(t, `INSERT INTO menu_translations (menu_id, language_code, label) VALUES ($1, 'id', 'Ekstra')`, id)

	// Act: requested language "en" has no translation for this menu -> falls back to "id" (default).
	p, err := f.svc.Profile(context.Background(), host, "en")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	found := false
	for _, m := range p.Menus {
		if m.Code == "extra" {
			found = true
			if m.Label != "Ekstra" {
				t.Errorf("label = %q, want the default-language fallback Ekstra", m.Label)
			}
		}
	}
	if !found {
		t.Fatal("extra menu missing")
	}

	// Act: no translation at all -> falls back to the code.
	f.exec(t, `DELETE FROM menu_translations WHERE menu_id = $1`, id)
	p, err = f.svc.Profile(context.Background(), host, "")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	for _, m := range p.Menus {
		if m.Code == "extra" && m.Label != "extra" {
			t.Errorf("label = %q, want the code as the final fallback", m.Label)
		}
	}
}

func TestProfile_seededPermissionsAndActions(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "ida@example.test")

	// Act
	p, err := f.svc.Profile(context.Background(), host, "")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	wantPerms := map[string]bool{"events:create": true, "profile:update": true}
	if len(p.Permissions) != len(wantPerms) {
		t.Errorf("permissions = %v, want %v", p.Permissions, wantPerms)
	}
	for _, perm := range p.Permissions {
		if !wantPerms[perm] {
			t.Errorf("unexpected permission %q", perm)
		}
	}
	events := p.Actions["events"]
	if len(events) != 1 || events[0].Code != "create" || events[0].Permission != "events:create" {
		t.Fatalf("actions[events] = %+v", events)
	}
	if events[0].Label != "Buat acara" {
		t.Errorf("label = %q, want the permission-specific translation", events[0].Label)
	}
	if events[0].Placement.Mobile != "fab" {
		t.Errorf("placement.mobile = %q, want fab", events[0].Placement.Mobile)
	}
	profileActions := p.Actions["profile"]
	if len(profileActions) != 1 || profileActions[0].Code != "update" {
		t.Fatalf("actions[profile] = %+v", profileActions)
	}
}

func TestProfile_noDropdownIsSeededYet(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "joko@example.test")

	// Act
	p, err := f.svc.Profile(context.Background(), host, "")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert
	if len(p.Dropdowns) != 0 {
		t.Errorf("dropdowns = %+v, want none", p.Dropdowns)
	}
}

func TestProfile_aSyntheticDropdownIsResolvedAndRoleFiltered(t *testing.T) {
	// Arrange: the mechanism is built and tested even though nothing is seeded in production.
	f := newFixture(t)
	host := f.host(t, "kiki@example.test")
	var dropdownID, itemA, itemB string
	f.scanRow(t, &dropdownID, `INSERT INTO dropdowns (code) VALUES ('status') RETURNING dropdown_id`)
	f.exec(t, `INSERT INTO dropdown_translations (dropdown_id, language_code, label) VALUES ($1, 'id', 'Status')`, dropdownID)
	f.scanRow(t, &itemA, `INSERT INTO dropdown_items (dropdown_id, value, sort_order, is_default) VALUES ($1, 'active', 1, true) RETURNING item_id`, dropdownID)
	f.scanRow(t, &itemB, `INSERT INTO dropdown_items (dropdown_id, value, sort_order) VALUES ($1, 'draft', 2) RETURNING item_id`, dropdownID)
	f.exec(t, `INSERT INTO dropdown_item_translations (item_id, language_code, label) VALUES ($1, 'id', 'Aktif'), ($2, 'id', 'Draf')`, itemA, itemB)
	f.exec(t, `INSERT INTO role_dropdowns (role_id, dropdown_item_id) SELECT role_id, $1 FROM roles WHERE code = 'host'`, itemA)

	// Act
	p, err := f.svc.Profile(context.Background(), host, "")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	// Assert: only the granted item, not the ungranted "draft" item.
	dd, ok := p.Dropdowns["status"]
	if !ok {
		t.Fatalf("dropdowns = %+v, missing status", p.Dropdowns)
	}
	if dd.Label != "Status" || len(dd.Items) != 1 || dd.Items[0].Value != "active" || !dd.Items[0].IsDefault {
		t.Errorf("dropdowns[status] = %+v", dd)
	}
}

func TestHas_grantedAndUngrantedPermissions(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "lia@example.test")

	// Act + Assert
	if ok, err := f.svc.Has(context.Background(), host, rbac.Permission{Resource: "events", Action: "create"}); err != nil || !ok {
		t.Errorf("Has(events:create) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := f.svc.Has(context.Background(), host, rbac.Permission{Resource: "events", Action: "delete"}); err != nil || ok {
		t.Errorf("Has(events:delete) = %v, %v; want false, nil", ok, err)
	}
	if ok, err := f.svc.Has(context.Background(), host, rbac.Permission{Resource: "nonsense", Action: "create"}); err != nil || ok {
		t.Errorf("Has(nonsense:create) = %v, %v; want false, nil", ok, err)
	}
	if ok, err := f.svc.Has(context.Background(), "00000000-0000-4000-8000-000000000000", rbac.Permission{Resource: "events", Action: "read"}); err != nil || ok {
		t.Errorf("Has for an unknown host = %v, %v; want false, nil", ok, err)
	}
}

func TestUpdatePreferences_setsAndValidates(t *testing.T) {
	// Arrange
	f := newFixture(t)
	host := f.host(t, "made@example.test")

	// Act
	theme, lang := "dark", "en"
	p, err := f.svc.UpdatePreferences(context.Background(), host, rbac.PreferencesInput{Theme: &theme, Language: &lang})
	if err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}

	// Assert
	if p.Preferences.Theme != "dark" || p.Preferences.ThemeSource != "user" {
		t.Errorf("theme = %q/%q, want dark/user", p.Preferences.Theme, p.Preferences.ThemeSource)
	}
	if p.Preferences.Language != "en" || p.Preferences.LanguageSource != "user" {
		t.Errorf("language = %q/%q, want en/user", p.Preferences.Language, p.Preferences.LanguageSource)
	}

	// Act + Assert: an invalid theme is rejected without changing anything.
	badTheme := "purple"
	if _, err := f.svc.UpdatePreferences(context.Background(), host, rbac.PreferencesInput{Theme: &badTheme}); !isValidationError(err) {
		t.Errorf("bad theme err = %v, want a ValidationError", err)
	}

	// Act + Assert: an unknown language is rejected.
	badLang := "xx"
	if _, err := f.svc.UpdatePreferences(context.Background(), host, rbac.PreferencesInput{Language: &badLang}); !isValidationError(err) {
		t.Errorf("bad language err = %v, want a ValidationError", err)
	}

	// Act + Assert: leaving a field nil keeps the previous value unchanged.
	p, err = f.svc.UpdatePreferences(context.Background(), host, rbac.PreferencesInput{})
	if err != nil {
		t.Fatalf("UpdatePreferences (no-op): %v", err)
	}
	if p.Preferences.Theme != "dark" || p.Preferences.Language != "en" {
		t.Errorf("preferences changed on a no-op update: %+v", p.Preferences)
	}
}

func isValidationError(err error) bool {
	_, ok := err.(*rbac.ValidationError)
	return ok
}
