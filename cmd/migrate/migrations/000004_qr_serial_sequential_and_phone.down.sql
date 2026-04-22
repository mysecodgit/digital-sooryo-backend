-- Best-effort rollback
ALTER TABLE qr_codes
  DROP COLUMN claimed_phone;

