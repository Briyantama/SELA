package rbac

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PostgresRepository implements Repository on the RBAC foundation tables (migrations 0004, 0005).
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a Repository backed by the given database.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// HostRoleAndPreferences returns ErrUnknownHost when the host id does not exist.
func (r *PostgresRepository) HostRoleAndPreferences(ctx context.Context, hostID string) (roleCode string, theme, language *string, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT role_code, theme_mode, language_code FROM hosts WHERE host_id = $1`, hostID,
	).Scan(&roleCode, &theme, &language)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil, ErrUnknownHost
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("host role and preferences: %w", err)
	}
	return roleCode, theme, language, nil
}

// RoleDefaults returns the role's own theme/language defaults (before any translation), so callers
// can resolve the viewing language before asking for translated text.
func (r *PostgresRepository) RoleDefaults(ctx context.Context, roleCode string) (theme, language *string, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT default_theme_mode, default_language_code FROM roles WHERE code = $1`, roleCode,
	).Scan(&theme, &language)
	if err != nil {
		return nil, nil, fmt.Errorf("role defaults: %w", err)
	}
	return theme, language, nil
}

// RoleName returns the role's translated name, falling back to the default language then the code.
func (r *PostgresRepository) RoleName(ctx context.Context, roleCode, lang, defaultLang string) (string, error) {
	var name string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(t.name, d.name, r.code)
		   FROM roles r
		   LEFT JOIN role_translations t ON t.role_id = r.role_id AND t.language_code = $2
		   LEFT JOIN role_translations d ON d.role_id = r.role_id AND d.language_code = $3
		  WHERE r.code = $1`, roleCode, lang, defaultLang,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("role name: %w", err)
	}
	return name, nil
}

func (r *PostgresRepository) GlobalDefaultTheme(ctx context.Context) (string, error) {
	var theme string
	if err := r.db.QueryRowContext(ctx, `SELECT default_theme_mode FROM app_settings`).Scan(&theme); err != nil {
		return "", fmt.Errorf("global default theme: %w", err)
	}
	return theme, nil
}

func (r *PostgresRepository) DefaultLanguage(ctx context.Context) (string, error) {
	var code string
	if err := r.db.QueryRowContext(ctx, `SELECT language_code FROM languages WHERE is_default`).Scan(&code); err != nil {
		return "", fmt.Errorf("default language: %w", err)
	}
	return code, nil
}

func (r *PostgresRepository) ActiveLanguages(ctx context.Context) ([]Language, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT language_code, native_name FROM languages WHERE is_active ORDER BY language_code`)
	if err != nil {
		return nil, fmt.Errorf("active languages: %w", err)
	}
	defer rows.Close()

	var out []Language
	for rows.Next() {
		var l Language
		if err := rows.Scan(&l.Code, &l.Name); err != nil {
			return nil, fmt.Errorf("scan language: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read languages: %w", err)
	}
	return out, nil
}

func (r *PostgresRepository) IsActiveLanguage(ctx context.Context, code string) (bool, error) {
	var active bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM languages WHERE language_code = $1 AND is_active)`, code,
	).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("check language: %w", err)
	}
	return active, nil
}

type menuRow struct {
	MenuID, Code, Type         string
	ParentID                   sql.NullString
	Path, Icon, Tooltip        sql.NullString
	SortOrder                  int
	HideOnMobile, HideOnTablet bool
	MobilePriority             sql.NullInt32
	Label                      string
}

// MenusForRole returns the role's granted menus plus every ancestor a granted menu needs to hang
// from, nested into a tree. A group left with no visible child is dropped.
func (r *PostgresRepository) MenusForRole(ctx context.Context, roleCode, lang, defaultLang string) ([]Menu, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH RECURSIVE granted AS (
			SELECT m.menu_id, m.parent_id, m.code, m.menu_type, m.path, m.icon,
			       COALESCE(rm.sort_order_override, m.sort_order) AS sort_order,
			       COALESCE(rm.hide_on_mobile_override, m.hide_on_mobile) AS hide_on_mobile,
			       COALESCE(rm.hide_on_tablet_override, m.hide_on_tablet) AS hide_on_tablet,
			       COALESCE(rm.mobile_priority_override, m.mobile_priority) AS mobile_priority
			  FROM role_menus rm
			  JOIN menus m ON m.menu_id = rm.menu_id
			  JOIN roles r ON r.role_id = rm.role_id
			 WHERE r.code = $1 AND rm.is_active AND m.is_active
		), visible AS (
			SELECT menu_id, parent_id, code, menu_type, path, icon, sort_order,
			       hide_on_mobile, hide_on_tablet, mobile_priority
			  FROM granted
			UNION
			SELECT m.menu_id, m.parent_id, m.code, m.menu_type, m.path, m.icon, m.sort_order,
			       m.hide_on_mobile, m.hide_on_tablet, m.mobile_priority
			  FROM menus m JOIN visible v ON m.menu_id = v.parent_id
			 WHERE m.is_active
		)
		SELECT v.menu_id, v.parent_id, v.code, v.menu_type, v.path, v.icon, v.sort_order,
		       v.hide_on_mobile, v.hide_on_tablet, v.mobile_priority,
		       COALESCE(t.label, d.label, v.code) AS label, COALESCE(t.tooltip, d.tooltip) AS tooltip
		  FROM visible v
		  LEFT JOIN menu_translations t ON t.menu_id = v.menu_id AND t.language_code = $2
		  LEFT JOIN menu_translations d ON d.menu_id = v.menu_id AND d.language_code = $3
		 ORDER BY v.sort_order, v.code`,
		roleCode, lang, defaultLang)
	if err != nil {
		return nil, fmt.Errorf("menus for role: %w", err)
	}
	defer rows.Close()

	var flat []menuRow
	for rows.Next() {
		var row menuRow
		if err := rows.Scan(&row.MenuID, &row.ParentID, &row.Code, &row.Type, &row.Path, &row.Icon,
			&row.SortOrder, &row.HideOnMobile, &row.HideOnTablet, &row.MobilePriority, &row.Label, &row.Tooltip); err != nil {
			return nil, fmt.Errorf("scan menu: %w", err)
		}
		flat = append(flat, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read menus: %w", err)
	}
	return buildMenuTree(flat), nil
}

type menuNode struct {
	row      menuRow
	children []string
}

// buildMenuTree assembles the flat, already-ordered rows (parents can appear after or before their
// children; order only matters within siblings) into a nested tree, dropping any empty group.
func buildMenuTree(rows []menuRow) []Menu {
	nodes := make(map[string]*menuNode, len(rows))
	for _, row := range rows {
		nodes[row.MenuID] = &menuNode{row: row}
	}
	var rootIDs []string
	for _, row := range rows {
		if row.ParentID.Valid {
			if parent, ok := nodes[row.ParentID.String]; ok {
				parent.children = append(parent.children, row.MenuID)
				continue
			}
		}
		rootIDs = append(rootIDs, row.MenuID)
	}

	var build func(id string) (Menu, bool)
	build = func(id string) (Menu, bool) {
		n := nodes[id]
		var children []Menu
		for _, childID := range n.children {
			if child, ok := build(childID); ok {
				children = append(children, child)
			}
		}
		if n.row.Type == "group" && len(children) == 0 {
			return Menu{}, false
		}
		m := Menu{
			Code: n.row.Code, Type: n.row.Type, Label: n.row.Label, SortOrder: n.row.SortOrder,
			Layout:   MenuLayout{HideOnMobile: n.row.HideOnMobile, HideOnTablet: n.row.HideOnTablet},
			Children: children,
		}
		if n.row.Tooltip.Valid {
			m.Tooltip = &n.row.Tooltip.String
		}
		if n.row.Path.Valid {
			m.Path = &n.row.Path.String
		}
		if n.row.Icon.Valid {
			m.Icon = &n.row.Icon.String
		}
		if n.row.MobilePriority.Valid {
			p := int(n.row.MobilePriority.Int32)
			m.Layout.MobilePriority = &p
		}
		return m, true
	}

	var roots []Menu
	for _, id := range rootIDs {
		if m, ok := build(id); ok {
			roots = append(roots, m)
		}
	}
	return roots
}

// ActionsForRole returns the role's granted actions grouped by resource code, and the flat
// "resource:action" permission list.
func (r *PostgresRepository) ActionsForRole(ctx context.Context, roleCode, lang, defaultLang string) (map[string][]ActionPermission, []string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT res.code, a.code, res.code || ':' || a.code AS permission,
		       COALESCE(pt.label, at1.label, at2.label, a.code) AS label,
		       COALESCE(at1.tooltip, at2.tooltip) AS tooltip,
		       COALESCE(at1.confirm_message, at2.confirm_message) AS confirm_message,
		       a.icon, a.variant, a.is_destructive, a.sort_order,
		       a.placement_desktop, a.placement_tablet, a.placement_mobile
		  FROM role_actions ra
		  JOIN roles r ON r.role_id = ra.role_id
		  JOIN permissions p ON p.permission_id = ra.permission_id AND p.is_active
		  JOIN resources res ON res.resource_id = p.resource_id AND res.is_active
		  JOIN actions a ON a.action_id = p.action_id AND a.is_active
		  LEFT JOIN permission_translations pt ON pt.permission_id = p.permission_id AND pt.language_code = $2
		  LEFT JOIN action_translations at1 ON at1.action_id = a.action_id AND at1.language_code = $2
		  LEFT JOIN action_translations at2 ON at2.action_id = a.action_id AND at2.language_code = $3
		 WHERE r.code = $1 AND ra.is_active
		 ORDER BY res.code, a.sort_order, a.code`,
		roleCode, lang, defaultLang)
	if err != nil {
		return nil, nil, fmt.Errorf("actions for role: %w", err)
	}
	defer rows.Close()

	actions := map[string][]ActionPermission{}
	var permissions []string
	for rows.Next() {
		var (
			resource               string
			a                      ActionPermission
			icon, tooltip, confirm sql.NullString
		)
		if err := rows.Scan(&resource, &a.Code, &a.Permission, &a.Label, &tooltip, &confirm,
			&icon, &a.Variant, &a.Destructive, &a.SortOrder,
			&a.Placement.Desktop, &a.Placement.Tablet, &a.Placement.Mobile); err != nil {
			return nil, nil, fmt.Errorf("scan action: %w", err)
		}
		if icon.Valid {
			a.Icon = icon.String
		}
		if tooltip.Valid {
			a.Tooltip = &tooltip.String
		}
		if confirm.Valid {
			a.ConfirmMessage = &confirm.String
		}
		actions[resource] = append(actions[resource], a)
		permissions = append(permissions, a.Permission)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read actions: %w", err)
	}
	return actions, permissions, nil
}

// DropdownsForRole returns every dropdown that has at least one item granted to the role, keyed by
// dropdown code.
func (r *PostgresRepository) DropdownsForRole(ctx context.Context, roleCode, lang, defaultLang string) (map[string]Dropdown, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT d.code, COALESCE(dt.label, ddt.label, d.code) AS label,
		       COALESCE(dt.placeholder, ddt.placeholder) AS placeholder,
		       d.is_searchable, d.mobile_presentation,
		       i.value, COALESCE(it.label, idt.label, i.value) AS item_label, i.icon, i.is_default,
		       COALESCE(rd.sort_order_override, i.sort_order) AS item_sort
		  FROM role_dropdowns rd
		  JOIN roles r ON r.role_id = rd.role_id
		  JOIN dropdown_items i ON i.item_id = rd.dropdown_item_id AND i.is_active
		  JOIN dropdowns d ON d.dropdown_id = i.dropdown_id AND d.is_active
		  LEFT JOIN dropdown_translations dt ON dt.dropdown_id = d.dropdown_id AND dt.language_code = $2
		  LEFT JOIN dropdown_translations ddt ON ddt.dropdown_id = d.dropdown_id AND ddt.language_code = $3
		  LEFT JOIN dropdown_item_translations it ON it.item_id = i.item_id AND it.language_code = $2
		  LEFT JOIN dropdown_item_translations idt ON idt.item_id = i.item_id AND idt.language_code = $3
		 WHERE r.code = $1 AND rd.is_active
		 ORDER BY d.code, item_sort, i.value`,
		roleCode, lang, defaultLang)
	if err != nil {
		return nil, fmt.Errorf("dropdowns for role: %w", err)
	}
	defer rows.Close()

	out := map[string]Dropdown{}
	for rows.Next() {
		var (
			code, label, itemValue, itemLabel string
			placeholder, itemIcon             sql.NullString
			searchable, isDefault             bool
			presentation                      string
			itemSort                          int
		)
		if err := rows.Scan(&code, &label, &placeholder, &searchable, &presentation,
			&itemValue, &itemLabel, &itemIcon, &isDefault, &itemSort); err != nil {
			return nil, fmt.Errorf("scan dropdown: %w", err)
		}
		dd, ok := out[code]
		if !ok {
			dd = Dropdown{Label: label, Searchable: searchable, MobilePresentation: presentation}
			if placeholder.Valid {
				dd.Placeholder = &placeholder.String
			}
		}
		item := DropdownItem{Value: itemValue, Label: itemLabel, IsDefault: isDefault, SortOrder: itemSort}
		if itemIcon.Valid {
			item.Icon = &itemIcon.String
		}
		dd.Items = append(dd.Items, item)
		out[code] = dd
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read dropdowns: %w", err)
	}
	return out, nil
}

// HasPermission is deny-by-default: any missing or inactive link (unknown host, inactive role,
// ungranted permission) answers false, never an error.
func (r *PostgresRepository) HasPermission(ctx context.Context, hostID, resource, action string) (bool, error) {
	var allowed bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM hosts h
			  JOIN roles r ON r.code = h.role_code AND r.is_active
			  JOIN role_actions ra ON ra.role_id = r.role_id AND ra.is_active
			  JOIN permissions p ON p.permission_id = ra.permission_id AND p.is_active
			  JOIN resources res ON res.resource_id = p.resource_id AND res.is_active AND res.code = $2
			  JOIN actions a ON a.action_id = p.action_id AND a.is_active AND a.code = $3
			 WHERE h.host_id = $1
		)`, hostID, resource, action).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("has permission: %w", err)
	}
	return allowed, nil
}

// UpdatePreferences leaves a preference unchanged when its argument is nil.
func (r *PostgresRepository) UpdatePreferences(ctx context.Context, hostID string, theme, language *string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE hosts SET theme_mode = COALESCE($2, theme_mode), language_code = COALESCE($3, language_code) WHERE host_id = $1`,
		hostID, theme, language)
	if err != nil {
		return fmt.Errorf("update preferences: %w", err)
	}
	return nil
}
