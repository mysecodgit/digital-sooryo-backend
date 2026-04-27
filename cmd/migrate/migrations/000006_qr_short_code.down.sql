ALTER TABLE qr_codes
  DROP INDEX uq_qr_short_code;

ALTER TABLE qr_codes
  DROP COLUMN short_code;

