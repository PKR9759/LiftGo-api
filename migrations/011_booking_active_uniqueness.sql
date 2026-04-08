-- Replace strict unique(ride_id, rider_id) with partial uniqueness for active booking states.
-- This allows a rider to rebook after cancellation/no_show/completion while still
-- preventing duplicate pending/confirmed flow bookings on the same ride.

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS unique_rider_per_ride;

CREATE UNIQUE INDEX IF NOT EXISTS ux_bookings_active_ride_rider
ON bookings (ride_id, rider_id)
WHERE status IN ('pending', 'confirmed', 'rider_ready', 'picked_up');
