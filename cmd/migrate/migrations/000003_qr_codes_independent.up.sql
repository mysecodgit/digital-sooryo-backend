-- Make QR codes independent of weddings:
-- - Each QR has its own amount and serial number
-- - QR codes start inactive (no active window)
-- - Selected codes can be activated for a time window (active_from/active_to)
-- - Claims no longer reference weddings

-- 1) qr_codes: drop wedding FK/index (name may differ), add lifecycle columns
SET @fk_qr_codes_wedding := (
  SELECT CONSTRAINT_NAME
  FROM information_schema.KEY_COLUMN_USAGE
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'qr_codes'
    AND COLUMN_NAME = 'wedding_id'
    AND REFERENCED_TABLE_NAME = 'weddings'
  LIMIT 1
);
SET @sql := IF(
  @fk_qr_codes_wedding IS NULL,
  'SELECT 1',
  CONCAT('ALTER TABLE qr_codes DROP FOREIGN KEY `', @fk_qr_codes_wedding, '`')
);
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @idx_qr_wedding := (
  SELECT INDEX_NAME
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'qr_codes'
    AND INDEX_NAME = 'idx_qr_wedding'
  LIMIT 1
);
SET @sql := IF(
  @idx_qr_wedding IS NULL,
  'SELECT 1',
  'ALTER TABLE qr_codes DROP INDEX idx_qr_wedding'
);
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- Add new columns first WITHOUT strict constraints so existing rows can be backfilled safely.
ALTER TABLE qr_codes
  ADD COLUMN serial_number VARCHAR(32) NULL AFTER id,
  ADD COLUMN amount DECIMAL(10,2) NOT NULL DEFAULT 0.00 AFTER token,
  ADD COLUMN active_from DATETIME NULL AFTER amount,
  ADD COLUMN active_to DATETIME NULL AFTER active_from,
  ADD COLUMN activated_at DATETIME NULL AFTER active_to;

-- Backfill serial numbers uniquely for existing rows.
UPDATE qr_codes
SET serial_number = CONCAT('QR', LPAD(id, 10, '0'))
WHERE serial_number IS NULL OR serial_number = '';

-- Backfill amount from the wedding configuration for existing rows.
UPDATE qr_codes qc
JOIN weddings w ON w.id = qc.wedding_id
SET qc.amount = w.amount_per_qr
WHERE (qc.amount IS NULL OR qc.amount = 0.00);

-- Now enforce constraints.
ALTER TABLE qr_codes
  MODIFY wedding_id INT UNSIGNED NULL,
  MODIFY serial_number VARCHAR(32) NOT NULL,
  ADD UNIQUE KEY uq_qr_serial_number (serial_number);

-- 2) claims: remove wedding_id and its constraints / uniqueness
SET @fk_claims_wedding := (
  SELECT CONSTRAINT_NAME
  FROM information_schema.KEY_COLUMN_USAGE
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'claims'
    AND COLUMN_NAME = 'wedding_id'
    AND REFERENCED_TABLE_NAME = 'weddings'
  LIMIT 1
);
SET @sql := IF(
  @fk_claims_wedding IS NULL,
  'SELECT 1',
  CONCAT('ALTER TABLE claims DROP FOREIGN KEY `', @fk_claims_wedding, '`')
);
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

ALTER TABLE claims
  DROP INDEX uq_claim_phone_wedding;

ALTER TABLE claims
  DROP COLUMN wedding_id;

