-- Add collision-free short_code to qr_codes for QR payloads.
-- We derive it from the auto-increment `id` using base36: UPPER(CONV(id,10,36)).

ALTER TABLE qr_codes
  ADD COLUMN short_code VARCHAR(12) NULL AFTER id;

-- Backfill existing rows.
UPDATE qr_codes
SET short_code = UPPER(CONV(id, 10, 36))
WHERE short_code IS NULL OR short_code = '';

-- Enforce uniqueness and not-null.
ALTER TABLE qr_codes
  MODIFY short_code VARCHAR(12) NOT NULL,
  ADD UNIQUE KEY uq_qr_short_code (short_code);

