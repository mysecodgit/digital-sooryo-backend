-- Speed up serial range selection and wedding assignment filtering
ALTER TABLE qr_codes
  ADD INDEX idx_qr_serial (serial_number),
  ADD INDEX idx_qr_wedding (wedding_id);

