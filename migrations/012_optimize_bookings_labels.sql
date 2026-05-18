-- Optimization: Cache ride labels at booking time
-- Avoids JOIN to rides table when fetching booking list
-- Trade-off: +2 columns, -1 JOIN per booking query = faster reads

ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS ride_origin_label TEXT,
    ADD COLUMN IF NOT EXISTS ride_dest_label TEXT;

COMMENT ON COLUMN bookings.ride_origin_label IS 'Cached from rides.origin_label at booking time for quick display';
COMMENT ON COLUMN bookings.ride_dest_label IS 'Cached from rides.dest_label at booking time for quick display';

-- Backfill existing bookings from rides table
UPDATE bookings b
SET 
    ride_origin_label = r.origin_label,
    ride_dest_label = r.dest_label
FROM rides r
WHERE b.ride_id = r.id
    AND b.ride_origin_label IS NULL;
