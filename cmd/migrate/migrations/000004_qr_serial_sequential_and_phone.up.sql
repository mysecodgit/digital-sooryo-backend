-- 1) Store claimant phone directly on qr_codes
ALTER TABLE qr_codes
  ADD COLUMN claimed_phone VARCHAR(40) NULL AFTER is_claimed;

-- 2) Convert existing serial numbers to sequential format: 0001, 0002, ...
-- (If id grows beyond 9999, it becomes 10000, 10001, ... automatically.)
UPDATE qr_codes
SET serial_number = LPAD(id, 4, '0')
WHERE serial_number IS NOT NULL;

