ALTER TABLE bookings DROP CONSTRAINT IF EXISTS valid_booking_status;
ALTER TABLE bookings ADD CONSTRAINT valid_booking_status CHECK (
    status IN ('pending','confirmed','rider_ready','picked_up','no_show','cancelled','completed')
);

ALTER TABLE bookings ADD COLUMN IF NOT EXISTS picked_up_at TIMESTAMPTZ;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS dropped_at TIMESTAMPTZ;

ALTER TABLE rides DROP CONSTRAINT IF EXISTS valid_ride_status;
ALTER TABLE rides ADD CONSTRAINT valid_ride_status CHECK (
    status IN ('scheduled','active','full','cancelled','completed')
);
ALTER TABLE rides ALTER COLUMN status SET DEFAULT 'scheduled';

-- Safe migration for existing active rides that haven't departed yet
UPDATE rides SET status = 'scheduled' WHERE status = 'active' AND departure_at > now();
