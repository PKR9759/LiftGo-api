CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS rides (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    origin_lat      NUMERIC(10,7) NOT NULL,
    origin_lng      NUMERIC(10,7) NOT NULL,
    origin_label    TEXT NOT NULL,

    dest_lat        NUMERIC(10,7) NOT NULL,
    dest_lng        NUMERIC(10,7) NOT NULL,
    dest_label      TEXT NOT NULL,

    route           geometry(LineString, 4326) NOT NULL,

    departure_at    TIMESTAMPTZ NOT NULL,
    total_seats     INT NOT NULL CHECK (total_seats > 0),
    available_seats INT NOT NULL CHECK (available_seats >= 0),
    price_per_seat  NUMERIC(10,2) NOT NULL DEFAULT 0,

    is_recurring    BOOLEAN NOT NULL DEFAULT false,
    recurrence_days INT[],

    notes           TEXT,
    status          TEXT NOT NULL DEFAULT 'active',

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT available_lte_total CHECK (available_seats <= total_seats),
    CONSTRAINT valid_ride_status   CHECK (status IN ('active','full','cancelled','completed'))
);

CREATE INDEX IF NOT EXISTS idx_rides_route     ON rides USING GIST(route);
CREATE INDEX IF NOT EXISTS idx_rides_driver    ON rides(driver_id);
CREATE INDEX IF NOT EXISTS idx_rides_departure ON rides(departure_at);
CREATE INDEX IF NOT EXISTS idx_rides_status    ON rides(status);