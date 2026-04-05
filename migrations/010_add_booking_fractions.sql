-- Step 2.1 - Add columns to bookings
ALTER TABLE bookings 
    ADD COLUMN IF NOT EXISTS pickup_fraction DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS dropoff_fraction DOUBLE PRECISION;

COMMENT ON COLUMN bookings.pickup_fraction IS 'position on driver''s route where passenger boards (0.0=start, 1.0=end)';
COMMENT ON COLUMN bookings.dropoff_fraction IS 'position on driver''s route where passenger exits';

-- Step 2.5 - Backfill existing bookings
-- Conservative approach: existing confirmed/active bookings occupy the full route
UPDATE bookings b
SET 
    pickup_fraction = 0.0,
    dropoff_fraction = 1.0
WHERE b.pickup_fraction IS NULL
    AND b.status IN ('confirmed', 'rider_ready', 'picked_up');
