-- Removes the unused seeks functionality
-- Seeks table was never populated after initial development

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS fk_seeks;
ALTER TABLE bookings DROP COLUMN IF EXISTS seek_id;
DROP TABLE IF EXISTS seeks;
