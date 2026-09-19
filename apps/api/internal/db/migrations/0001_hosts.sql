-- +goose Up
-- Hosts authenticate with an email or phone one-time code (FSD 4.1, 8.6), so there is no password column.
-- Column-level encryption of email/phone (FSD 8.3) is a later milestone; it will need a blind index for the unique lookups below.
CREATE TABLE hosts (
    host_id    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text,
    email      text,
    phone      text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT hosts_contact_present CHECK (email IS NOT NULL OR phone IS NOT NULL),
    CONSTRAINT hosts_email_not_empty CHECK (email IS NULL OR email <> ''),
    CONSTRAINT hosts_phone_not_empty CHECK (phone IS NULL OR phone <> '')
);

CREATE UNIQUE INDEX hosts_email_key ON hosts (lower(email)) WHERE email IS NOT NULL;
CREATE UNIQUE INDEX hosts_phone_key ON hosts (phone) WHERE phone IS NOT NULL;

-- +goose Down
DROP TABLE hosts;
