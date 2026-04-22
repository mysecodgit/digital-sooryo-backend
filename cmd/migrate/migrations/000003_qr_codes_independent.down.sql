-- Best-effort rollback for 000003_qr_codes_independent.up.sql

-- 1) claims: re-add wedding_id (nullable) and uniqueness per wedding+phone.
ALTER TABLE claims
  ADD COLUMN wedding_id INT UNSIGNED NULL AFTER qr_id;

ALTER TABLE claims
  ADD UNIQUE KEY uq_claim_phone_wedding (phone, wedding_id);

ALTER TABLE claims
  ADD CONSTRAINT fk_claims_wedding FOREIGN KEY (wedding_id) REFERENCES weddings(id) ON DELETE CASCADE;

-- 2) qr_codes: drop lifecycle columns, restore wedding FK + index, make wedding_id required.
SET @idx_serial := (
  SELECT INDEX_NAME
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'qr_codes'
    AND INDEX_NAME = 'uq_qr_serial_number'
  LIMIT 1
);
SET @sql := IF(@idx_serial IS NULL, 'SELECT 1', 'ALTER TABLE qr_codes DROP INDEX uq_qr_serial_number');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

ALTER TABLE qr_codes
  DROP COLUMN activated_at,
  DROP COLUMN active_to,
  DROP COLUMN active_from,
  DROP COLUMN amount,
  DROP COLUMN serial_number;

ALTER TABLE qr_codes
  MODIFY wedding_id INT UNSIGNED NOT NULL;

ALTER TABLE qr_codes
  ADD INDEX idx_qr_wedding (wedding_id);

ALTER TABLE qr_codes
  ADD CONSTRAINT fk_qr_codes_wedding FOREIGN KEY (wedding_id) REFERENCES weddings(id) ON DELETE CASCADE;

