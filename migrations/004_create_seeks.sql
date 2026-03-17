CREATE TABLE IF NOT EXISTS seeks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seeker_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    origin_lat   NUMERIC(10,7) NOT NULL,
    origin_lng   NUMERIC(10,7) NOT NULL,
    origin_label TEXT NOT NULL,

    dest_lat     NUMERIC(10,7) NOT NULL,
    dest_lng     NUMERIC(10,7) NOT NULL,
    dest_label   TEXT NOT NULL,

    route        GEOMETRY(LINESTRING, 4326) NOT NULL,

    seats_needed INT NOT NULL DEFAULT 1 CHECK (seats_needed > 0),

    is_recurring    BOOLEAN NOT NULL DEFAULT false,
    recurrence_days INT[],

    status     TEXT NOT NULL DEFAULT 'active',
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '2 hours',

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT valid_seek_status CHECK (status IN ('active','matched','expired','cancelled'))
);

CREATE INDEX IF NOT EXISTS idx_seeks_route   ON seeks USING GIST(route);
CREATE INDEX IF NOT EXISTS idx_seeks_seeker  ON seeks(seeker_id);
CREATE INDEX IF NOT EXISTS idx_seeks_status  ON seeks(status);
CREATE INDEX IF NOT EXISTS idx_seeks_expires ON seeks(expires_at);