-- +goose Up
-- FSD 5 entities GuestSession and Media, plus the minor-protection flag on events (FSD 8.11).
-- Column names are English forms: nama_panggilan -> nickname, dibuat_pada -> created_at, tipe -> kind,
-- url_storage -> object_key (a storage key, never a URL: media is served only via short-lived signed URLs),
-- status (aktif/disembunyikan/dihapus) -> status (active/hidden/deleted), is_hall_of_fame -> hall_of_fame,
-- urutan_hall_of_fame -> hall_of_fame_rank, jumlah_reaksi -> reaction_count, diunggah_pada -> uploaded_at.
--
-- Deviations from FSD 5, each for a stated reason:
-- * guest_sessions has no sisa_jepretan column. The live counter is in Redis (FSD 2.4); the durable count is
--   derived from media rows, so there is no second counter to drift.
-- * guest_sessions has no browser fingerprint. Data minimisation for events with children (FSD 8.11); a
--   device is identified only by its session cookie.
-- * media gets processing_state (pending/ready/failed), separate from the moderation status: an upload
--   exists as a row from the moment its signed upload URL is issued, and is served only once processed
--   (EXIF/GPS stripped, magic bytes checked: FR-SEC.2, FR-SEC.3).

-- Events that involve children are private and never indexable (FSD 8.11). Set by the host; off by default.
ALTER TABLE events ADD COLUMN involves_minors boolean NOT NULL DEFAULT false;

CREATE TABLE guest_sessions (
    session_id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id     uuid NOT NULL REFERENCES events (event_id) ON DELETE RESTRICT,
    nickname     text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT guest_sessions_nickname_length CHECK (
        nickname IS NULL OR (btrim(nickname) <> '' AND char_length(nickname) <= 40)
    )
);

-- Target of media's composite foreign key: a guest's media must belong to the same event as the session.
CREATE UNIQUE INDEX guest_sessions_event_session_key ON guest_sessions (event_id, session_id);

CREATE TABLE media (
    media_id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id          uuid NOT NULL REFERENCES events (event_id) ON DELETE RESTRICT,
    -- NULL means the host uploaded it (FSD 6: POST /events/{id}/media is for guests and the host).
    session_id        uuid,
    kind              text NOT NULL,
    content_type      text NOT NULL,
    object_key        text NOT NULL,
    size_bytes        bigint,
    processing_state  text NOT NULL DEFAULT 'pending',
    status            text NOT NULL DEFAULT 'active',
    hall_of_fame      boolean NOT NULL DEFAULT false,
    hall_of_fame_rank integer,
    reaction_count    integer NOT NULL DEFAULT 0,
    uploaded_at       timestamptz NOT NULL DEFAULT now(),
    ready_at          timestamptz,
    deleted_at        timestamptz,
    CONSTRAINT media_session_same_event FOREIGN KEY (event_id, session_id)
        REFERENCES guest_sessions (event_id, session_id) ON DELETE RESTRICT,
    CONSTRAINT media_kind_valid CHECK (kind IN ('photo', 'video')),
    -- FR-04.1 formats (JPEG/PNG/HEIC, MP4/MOV) plus WebP, which the camera pipeline outputs (FSD 7).
    CONSTRAINT media_content_type_valid CHECK (
        (kind = 'photo' AND content_type IN ('image/jpeg', 'image/png', 'image/heic', 'image/webp'))
        OR (kind = 'video' AND content_type IN ('video/mp4', 'video/quicktime'))
    ),
    CONSTRAINT media_object_key_not_empty CHECK (btrim(object_key) <> ''),
    CONSTRAINT media_size_positive CHECK (size_bytes IS NULL OR size_bytes > 0),
    CONSTRAINT media_processing_state_valid CHECK (processing_state IN ('pending', 'ready', 'failed')),
    CONSTRAINT media_ready_is_complete CHECK (
        processing_state <> 'ready' OR (size_bytes IS NOT NULL AND ready_at IS NOT NULL)
    ),
    CONSTRAINT media_status_valid CHECK (status IN ('active', 'hidden', 'deleted')),
    CONSTRAINT media_deleted_has_time CHECK ((status = 'deleted') = (deleted_at IS NOT NULL)),
    -- Only a visible, processed item can be a highlight; hiding or deleting one must un-highlight it first.
    CONSTRAINT media_hall_of_fame_is_visible CHECK (
        NOT hall_of_fame OR (status = 'active' AND processing_state = 'ready')
    ),
    CONSTRAINT media_hall_of_fame_rank_set CHECK (hall_of_fame = (hall_of_fame_rank IS NOT NULL)),
    CONSTRAINT media_hall_of_fame_rank_positive CHECK (hall_of_fame_rank IS NULL OR hall_of_fame_rank > 0),
    CONSTRAINT media_reaction_count_non_negative CHECK (reaction_count >= 0)
);

CREATE UNIQUE INDEX media_object_key_key ON media (object_key);
-- Host gallery: every non-deleted item of one event, newest first (FR-05.6).
CREATE INDEX media_event_uploaded_idx ON media (event_id, uploaded_at DESC, media_id DESC)
    WHERE status <> 'deleted';
-- Guest gallery: one session's own items (FR-05.5); also counts shots per session.
CREATE INDEX media_event_session_idx ON media (event_id, session_id, uploaded_at DESC)
    WHERE session_id IS NOT NULL;
-- Hall of Fame in the host's order; one item per rank.
CREATE UNIQUE INDEX media_hall_of_fame_rank_key ON media (event_id, hall_of_fame_rank)
    WHERE hall_of_fame;

-- +goose Down
DROP TABLE media;
DROP TABLE guest_sessions;
ALTER TABLE events DROP COLUMN involves_minors;
