-- Default admin for local auth (password: password123 — change in production)
INSERT INTO users (name, email, password, role)
VALUES (
  'Admin',
  'admin@digitalsooryo.so',
  '$2a$10$vSy21vpT1RVDfhVU9AHn4uUmL9713fziz7EYARCxokJVF6uIk9n12',
  'admin'
);
