ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS pickup_label TEXT,
    ADD COLUMN IF NOT EXISTS dropoff_label TEXT;
