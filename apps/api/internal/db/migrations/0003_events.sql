-- +goose Up
-- Column names are English forms of the FSD 5 entity: kategori_acara -> category_code, nama_acara -> name,
-- tanggal_acara -> event_date, token_qr -> access_token, batas_jepretan -> shot_limit (NULL = unlimited),
-- mode_reveal/waktu_reveal -> reveal_mode/reveal_at, paket -> package, masa_berlaku -> expires_at.
--
-- Every event belongs to exactly one host. Queries for a host's events filter by (event_id, host_id)
-- or (host_id, created_at), so the access boundary is enforceable in SQL from day one (FSD 3.4).
CREATE TABLE events (
    event_id      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id       uuid NOT NULL REFERENCES hosts (host_id) ON DELETE RESTRICT,
    category_code text NOT NULL REFERENCES event_categories (code),
    name          text NOT NULL,
    event_date    date NOT NULL,
    timezone      text NOT NULL DEFAULT 'Asia/Jakarta',
    status        text NOT NULL DEFAULT 'active',
    access_token  text NOT NULL,
    short_code    text NOT NULL,
    shot_limit    integer,
    reveal_mode   text NOT NULL,
    reveal_at     timestamptz,
    package       text NOT NULL DEFAULT 'gratis',
    expires_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT events_name_not_empty CHECK (btrim(name) <> ''),
    CONSTRAINT events_status_valid CHECK (status IN ('draft', 'active', 'expired')),
    CONSTRAINT events_package_valid CHECK (package IN ('gratis', 'hemat', 'standar', 'premium', 'partner')),
    CONSTRAINT events_short_code_format CHECK (short_code ~ '^[0-9A-Za-z]{8}$'),
    CONSTRAINT events_shot_limit_positive CHECK (shot_limit IS NULL OR shot_limit > 0),
    CONSTRAINT events_reveal_mode_valid CHECK (reveal_mode IN ('instant', 'delayed')),
    CONSTRAINT events_delayed_reveal_has_time CHECK (reveal_mode <> 'delayed' OR reveal_at IS NOT NULL)
);

CREATE UNIQUE INDEX events_short_code_key ON events (short_code);
CREATE UNIQUE INDEX events_access_token_key ON events (access_token);
CREATE INDEX events_host_created_idx ON events (host_id, created_at DESC);

-- +goose Down
DROP TABLE events;
