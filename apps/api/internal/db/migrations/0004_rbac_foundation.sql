-- +goose Up
-- RBAC foundation: roles, translated menus/dropdowns/actions, and per-role grants, so the host web
-- app's navigation, permitted actions and theme/language defaults are data, not frontend code.
-- Every host today is exactly one role ("host"); the schema supports more without a code change.
CREATE TABLE app_settings (
    singleton          boolean PRIMARY KEY DEFAULT true,
    default_theme_mode text NOT NULL DEFAULT 'light',
    CONSTRAINT app_settings_singleton_true CHECK (singleton),
    CONSTRAINT app_settings_theme_valid CHECK (default_theme_mode IN ('light', 'dark'))
);
INSERT INTO app_settings (singleton) VALUES (true);

CREATE TABLE languages (
    language_code text PRIMARY KEY,
    name          text NOT NULL,
    native_name   text NOT NULL,
    is_default    boolean NOT NULL DEFAULT false,
    is_active     boolean NOT NULL DEFAULT true,
    CONSTRAINT languages_code_format CHECK (language_code ~ '^[a-z]{2,3}(-[A-Z]{2})?$')
);
-- Exactly one default language: the fallback for any missing translation.
CREATE UNIQUE INDEX languages_single_default ON languages ((true)) WHERE is_default;

INSERT INTO languages (language_code, name, native_name, is_default) VALUES
    ('id', 'Indonesian', 'Bahasa Indonesia', true),
    ('en', 'English', 'English', false);

CREATE TABLE roles (
    role_id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                  text NOT NULL UNIQUE,
    default_theme_mode    text,
    default_language_code text REFERENCES languages (language_code),
    is_system             boolean NOT NULL DEFAULT false,
    is_active             boolean NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT roles_code_format CHECK (code ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT roles_theme_valid CHECK (default_theme_mode IS NULL OR default_theme_mode IN ('light', 'dark'))
);

INSERT INTO roles (code, is_system) VALUES ('host', true);

CREATE TABLE role_translations (
    role_id       uuid NOT NULL REFERENCES roles (role_id) ON DELETE CASCADE,
    language_code text NOT NULL REFERENCES languages (language_code),
    name          text NOT NULL,
    description   text,
    PRIMARY KEY (role_id, language_code)
);

INSERT INTO role_translations (role_id, language_code, name, description)
SELECT role_id, 'id', 'Host', 'Pemilik acara' FROM roles WHERE code = 'host'
UNION ALL
SELECT role_id, 'en', 'Host', 'Event owner' FROM roles WHERE code = 'host';

CREATE TABLE menus (
    menu_id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id       uuid REFERENCES menus (menu_id) ON DELETE CASCADE,
    code            text NOT NULL UNIQUE,
    menu_type       text NOT NULL DEFAULT 'link',
    path            text,
    icon            text,
    sort_order      integer NOT NULL DEFAULT 0,
    hide_on_mobile  boolean NOT NULL DEFAULT false,
    hide_on_tablet  boolean NOT NULL DEFAULT false,
    mobile_priority smallint,
    is_active       boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT menus_code_format CHECK (code ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT menus_type_valid CHECK (menu_type IN ('link', 'group', 'divider')),
    CONSTRAINT menus_link_has_path CHECK (menu_type <> 'link' OR path IS NOT NULL),
    CONSTRAINT menus_non_link_has_no_path CHECK (menu_type = 'link' OR path IS NULL),
    CONSTRAINT menus_priority_range CHECK (mobile_priority IS NULL OR mobile_priority BETWEEN 1 AND 5),
    CONSTRAINT menus_not_own_parent CHECK (parent_id IS DISTINCT FROM menu_id)
);
CREATE INDEX menus_parent_idx ON menus (parent_id, sort_order);

-- A menu may never become its own ancestor. The only trigger in this schema: every other rule here
-- is a CHECK constraint, but this one needs to walk the tree.
-- +goose StatementBegin
CREATE FUNCTION menus_prevent_cycle() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.parent_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF EXISTS (
        WITH RECURSIVE up AS (
            SELECT menu_id, parent_id FROM menus WHERE menu_id = NEW.parent_id
            UNION
            SELECT m.menu_id, m.parent_id FROM menus m JOIN up ON m.menu_id = up.parent_id
        )
        SELECT 1 FROM up WHERE menu_id = NEW.menu_id
    ) THEN
        RAISE EXCEPTION 'menu "%" cannot be its own ancestor', NEW.code USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER menus_no_cycle BEFORE INSERT OR UPDATE OF parent_id ON menus
    FOR EACH ROW EXECUTE FUNCTION menus_prevent_cycle();

-- Seed: the two routes that exist today. events/new is the existing create-event flow; settings is
-- the new preferences screen added alongside this migration.
INSERT INTO menus (code, menu_type, path, icon, sort_order, mobile_priority) VALUES
    ('events_new', 'link', '/events/new', 'plus-circle', 10, 1),
    ('settings',   'link', '/settings',   'cog',         20, 2);

CREATE TABLE menu_translations (
    menu_id       uuid NOT NULL REFERENCES menus (menu_id) ON DELETE CASCADE,
    language_code text NOT NULL REFERENCES languages (language_code),
    label         text NOT NULL,
    tooltip       text,
    PRIMARY KEY (menu_id, language_code)
);

INSERT INTO menu_translations (menu_id, language_code, label, tooltip)
SELECT m.menu_id, v.lang, v.label, v.tooltip
FROM (VALUES
    ('events_new', 'id', 'Buat Acara Baru', 'Buat acara baru'),
    ('events_new', 'en', 'Create Event', 'Create a new event'),
    ('settings',   'id', 'Pengaturan', NULL),
    ('settings',   'en', 'Settings', NULL)
) AS v(code, lang, label, tooltip)
JOIN menus m ON m.code = v.code;

CREATE TABLE role_menus (
    role_id                  uuid NOT NULL REFERENCES roles (role_id) ON DELETE CASCADE,
    menu_id                  uuid NOT NULL REFERENCES menus (menu_id) ON DELETE CASCADE,
    is_active                boolean NOT NULL DEFAULT true,
    sort_order_override      integer,
    hide_on_mobile_override  boolean,
    hide_on_tablet_override  boolean,
    mobile_priority_override smallint,
    PRIMARY KEY (role_id, menu_id),
    CONSTRAINT role_menus_priority_range CHECK (mobile_priority_override IS NULL OR mobile_priority_override BETWEEN 1 AND 5)
);
CREATE INDEX role_menus_menu_idx ON role_menus (menu_id);

INSERT INTO role_menus (role_id, menu_id)
SELECT r.role_id, m.menu_id FROM roles r JOIN menus m ON true WHERE r.code = 'host';

-- Dropdowns: schema only, no seed rows. Nothing in the product needs a role-configurable dropdown
-- yet (event categories are their own richer, already-shipped system); this exists ready for when
-- one does.
CREATE TABLE dropdowns (
    dropdown_id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                text NOT NULL UNIQUE,
    mobile_presentation text NOT NULL DEFAULT 'bottom_sheet',
    is_searchable       boolean NOT NULL DEFAULT false,
    is_active           boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT dropdowns_code_format CHECK (code ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT dropdowns_presentation_valid CHECK (mobile_presentation IN ('inline', 'bottom_sheet', 'modal'))
);

CREATE TABLE dropdown_translations (
    dropdown_id   uuid NOT NULL REFERENCES dropdowns (dropdown_id) ON DELETE CASCADE,
    language_code text NOT NULL REFERENCES languages (language_code),
    label         text NOT NULL,
    placeholder   text,
    PRIMARY KEY (dropdown_id, language_code)
);

CREATE TABLE dropdown_items (
    item_id     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dropdown_id uuid NOT NULL REFERENCES dropdowns (dropdown_id) ON DELETE CASCADE,
    value       text NOT NULL,
    icon        text,
    sort_order  integer NOT NULL DEFAULT 0,
    is_default  boolean NOT NULL DEFAULT false,
    is_active   boolean NOT NULL DEFAULT true,
    metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (dropdown_id, value)
);
CREATE UNIQUE INDEX dropdown_items_single_default ON dropdown_items (dropdown_id) WHERE is_default;

CREATE TABLE dropdown_item_translations (
    item_id       uuid NOT NULL REFERENCES dropdown_items (item_id) ON DELETE CASCADE,
    language_code text NOT NULL REFERENCES languages (language_code),
    label         text NOT NULL,
    description   text,
    PRIMARY KEY (item_id, language_code)
);

CREATE TABLE role_dropdowns (
    role_id             uuid NOT NULL REFERENCES roles (role_id) ON DELETE CASCADE,
    dropdown_item_id    uuid NOT NULL REFERENCES dropdown_items (item_id) ON DELETE CASCADE,
    is_active           boolean NOT NULL DEFAULT true,
    sort_order_override integer,
    PRIMARY KEY (role_id, dropdown_item_id)
);
CREATE INDEX role_dropdowns_item_idx ON role_dropdowns (dropdown_item_id);

-- Actions/permissions: permission = resource x action (e.g. events:create). actions is a reusable
-- verb vocabulary; only the two permissions this change actually enforces are granted below.
CREATE TABLE resources (
    resource_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code        text NOT NULL UNIQUE,
    description text,
    is_active   boolean NOT NULL DEFAULT true,
    CONSTRAINT resources_code_format CHECK (code ~ '^[a-z][a-z0-9_]*$')
);

INSERT INTO resources (code, description) VALUES
    ('events',  'Events created by hosts'),
    ('profile', 'A host''s own RBAC profile and preferences');

CREATE TABLE actions (
    action_id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code              text NOT NULL UNIQUE,
    icon              text,
    variant           text NOT NULL DEFAULT 'neutral',
    is_destructive    boolean NOT NULL DEFAULT false,
    sort_order        integer NOT NULL DEFAULT 0,
    placement_desktop text NOT NULL DEFAULT 'toolbar',
    placement_tablet  text NOT NULL DEFAULT 'toolbar_icon',
    placement_mobile  text NOT NULL DEFAULT 'overflow',
    is_active         boolean NOT NULL DEFAULT true,
    CONSTRAINT actions_code_format CHECK (code ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT actions_variant_valid CHECK (variant IN ('primary', 'secondary', 'neutral', 'danger')),
    CONSTRAINT actions_placement_desktop_valid
        CHECK (placement_desktop IN ('toolbar', 'toolbar_icon', 'row', 'overflow', 'fab', 'swipe', 'hidden')),
    CONSTRAINT actions_placement_tablet_valid
        CHECK (placement_tablet IN ('toolbar', 'toolbar_icon', 'row', 'overflow', 'fab', 'swipe', 'hidden')),
    CONSTRAINT actions_placement_mobile_valid
        CHECK (placement_mobile IN ('toolbar', 'toolbar_icon', 'row', 'overflow', 'fab', 'swipe', 'hidden'))
);

INSERT INTO actions (code, icon, variant, is_destructive, sort_order, placement_desktop, placement_tablet, placement_mobile) VALUES
    ('create', 'plus',     'primary',   false, 10, 'toolbar', 'toolbar_icon', 'fab'),
    ('read',   'eye',      'neutral',   false, 20, 'row',     'row',          'row'),
    ('update', 'pencil',   'neutral',   false, 30, 'toolbar', 'toolbar_icon', 'swipe'),
    ('delete', 'trash-2',  'danger',    true,  40, 'toolbar', 'overflow',     'swipe'),
    ('export', 'download', 'secondary', false, 50, 'toolbar', 'toolbar_icon', 'overflow');

CREATE TABLE action_translations (
    action_id       uuid NOT NULL REFERENCES actions (action_id) ON DELETE CASCADE,
    language_code   text NOT NULL REFERENCES languages (language_code),
    label           text NOT NULL,
    tooltip         text,
    confirm_message text,
    PRIMARY KEY (action_id, language_code)
);

INSERT INTO action_translations (action_id, language_code, label, tooltip, confirm_message)
SELECT a.action_id, v.lang, v.label, v.tooltip, v.confirm_message
FROM (VALUES
    ('create', 'id', 'Buat',   'Buat data baru', NULL),
    ('create', 'en', 'Create', 'Create a new record', NULL),
    ('read',   'id', 'Lihat',  NULL, NULL),
    ('read',   'en', 'View',   NULL, NULL),
    ('update', 'id', 'Ubah',   NULL, NULL),
    ('update', 'en', 'Edit',   NULL, NULL),
    ('delete', 'id', 'Hapus',  NULL, 'Hapus data ini? Tindakan ini tidak dapat dibatalkan.'),
    ('delete', 'en', 'Delete', NULL, 'Delete this record? This cannot be undone.'),
    ('export', 'id', 'Ekspor', 'Unduh sebagai berkas', NULL),
    ('export', 'en', 'Export', 'Download as a file', NULL)
) AS v(code, lang, label, tooltip, confirm_message)
JOIN actions a ON a.code = v.code;

CREATE TABLE permissions (
    permission_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id   uuid NOT NULL REFERENCES resources (resource_id) ON DELETE CASCADE,
    action_id     uuid NOT NULL REFERENCES actions (action_id) ON DELETE CASCADE,
    is_active     boolean NOT NULL DEFAULT true,
    UNIQUE (resource_id, action_id)
);

-- Only the permissions this change actually enforces: POST /api/v1/events (events:create) and
-- PATCH /api/v1/me/preferences (profile:update).
INSERT INTO permissions (resource_id, action_id)
SELECT res.resource_id, a.action_id
FROM (VALUES ('events', 'create'), ('profile', 'update')) AS v(resource, action)
JOIN resources res ON res.code = v.resource
JOIN actions a ON a.code = v.action;

CREATE TABLE permission_translations (
    permission_id uuid NOT NULL REFERENCES permissions (permission_id) ON DELETE CASCADE,
    language_code text NOT NULL REFERENCES languages (language_code),
    label         text NOT NULL,
    PRIMARY KEY (permission_id, language_code)
);

INSERT INTO permission_translations (permission_id, language_code, label)
SELECT p.permission_id, v.lang, v.label
FROM (VALUES
    ('events', 'create', 'id', 'Buat acara'),
    ('events', 'create', 'en', 'Create event'),
    ('profile', 'update', 'id', 'Ubah preferensi'),
    ('profile', 'update', 'en', 'Update preferences')
) AS v(resource, action, lang, label)
JOIN resources res ON res.code = v.resource
JOIN actions a ON a.code = v.action
JOIN permissions p ON p.resource_id = res.resource_id AND p.action_id = a.action_id;

CREATE TABLE role_actions (
    role_id       uuid NOT NULL REFERENCES roles (role_id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions (permission_id) ON DELETE CASCADE,
    is_active     boolean NOT NULL DEFAULT true,
    granted_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (role_id, permission_id)
);
CREATE INDEX role_actions_permission_idx ON role_actions (permission_id);

INSERT INTO role_actions (role_id, permission_id)
SELECT r.role_id, p.permission_id FROM roles r JOIN permissions p ON true WHERE r.code = 'host';

-- +goose Down
DROP TABLE role_actions;
DROP TABLE permission_translations;
DROP TABLE permissions;
DROP TABLE action_translations;
DROP TABLE actions;
DROP TABLE resources;
DROP TABLE role_dropdowns;
DROP TABLE dropdown_item_translations;
DROP TABLE dropdown_items;
DROP TABLE dropdown_translations;
DROP TABLE dropdowns;
DROP TABLE role_menus;
DROP TABLE menu_translations;
DROP TABLE menus;
DROP FUNCTION menus_prevent_cycle();
DROP TABLE role_translations;
DROP TABLE roles;
DROP TABLE languages;
DROP TABLE app_settings;
