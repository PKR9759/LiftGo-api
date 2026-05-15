-- Remove decommissioned seek_id FK from bookings table
ALTER TABLE bookings DROP COLUMN IF EXISTS seek_id;

-- Drop seeks table if it still exists
DROP TABLE IF EXISTS seeks;

-- Update status check constraint to align with migration 008 (idempotent)
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS valid_booking_status;
ALTER TABLE bookings ADD CONSTRAINT valid_booking_status CHECK (
    status IN ('pending','confirmed','rider_ready','picked_up','no_show','cancelled','completed')
);

-- Store rider's actual pickup/dropoff coordinates (audit fix #26)
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS pickup_lat  DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS pickup_lng  DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS dropoff_lat DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS dropoff_lng DOUBLE PRECISION;
