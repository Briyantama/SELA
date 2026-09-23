-- +goose Up
-- Every host acts as exactly one role today; theme_mode/language_code are per-host overrides
-- (NULL = inherit from the role default, then the global default in app_settings/languages).
ALTER TABLE hosts
    ADD COLUMN role_code     text NOT NULL DEFAULT 'host' REFERENCES roles (code),
    ADD COLUMN theme_mode    text,
    ADD COLUMN language_code text REFERENCES languages (language_code),
    ADD CONSTRAINT hosts_theme_valid CHECK (theme_mode IS NULL OR theme_mode IN ('light', 'dark'));

-- +goose Down
ALTER TABLE hosts
    DROP CONSTRAINT hosts_theme_valid,
    DROP COLUMN language_code,
    DROP COLUMN theme_mode,
    DROP COLUMN role_code;
