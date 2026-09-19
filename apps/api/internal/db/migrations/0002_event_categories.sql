-- +goose Up
-- Categories are presets (theme + default settings), never feature gates (FSD 4.1 business rules).
-- A default is 'tbd' when the product docs do not define it yet; those rows carry no number so
-- nothing is invented (decision D5). Product must fill them in before beta.
CREATE TABLE event_categories (
    code                       text PRIMARY KEY,
    name                       text NOT NULL,
    theme_key                  text NOT NULL,
    shot_limit_default         text NOT NULL,
    default_shot_limit         integer,
    reveal_mode_default        text NOT NULL,
    default_reveal_delay_hours integer,
    CONSTRAINT event_categories_name_not_empty CHECK (btrim(name) <> ''),
    CONSTRAINT event_categories_theme_not_empty CHECK (btrim(theme_key) <> ''),
    CONSTRAINT event_categories_shot_default_valid CHECK (shot_limit_default IN ('unlimited', 'limited', 'tbd')),
    CONSTRAINT event_categories_shot_number CHECK (
        (shot_limit_default = 'limited') = (default_shot_limit IS NOT NULL)
        AND (default_shot_limit IS NULL OR default_shot_limit > 0)
    ),
    CONSTRAINT event_categories_reveal_default_valid CHECK (reveal_mode_default IN ('instant', 'delayed', 'tbd')),
    CONSTRAINT event_categories_reveal_delay CHECK (
        (reveal_mode_default = 'delayed') = (default_reveal_delay_hours IS NOT NULL)
        AND (default_reveal_delay_hours IS NULL OR default_reveal_delay_hours > 0)
    )
);

-- Documented defaults only (FSD 4.5): wedding reveals after 1 day; graduation and gathering reveal instantly.
-- Shot-limit defaults are undocumented as concrete numbers, so every category is 'tbd'.
INSERT INTO event_categories
    (code, name, theme_key, shot_limit_default, reveal_mode_default, default_reveal_delay_hours)
VALUES
    ('pernikahan',  'Pernikahan',            'wedding',    'tbd', 'delayed', 24),
    ('wisuda',      'Wisuda',                'graduation', 'tbd', 'instant', NULL),
    ('ulang_tahun', 'Ulang Tahun',           'birthday',   'tbd', 'tbd',     NULL),
    ('gathering',   'Gathering / Komunitas', 'gathering',  'tbd', 'instant', NULL),
    ('reuni',       'Reuni',                 'reunion',    'tbd', 'tbd',     NULL),
    ('lainnya',     'Lainnya',               'neutral',    'tbd', 'tbd',     NULL);

-- +goose Down
DROP TABLE event_categories;
