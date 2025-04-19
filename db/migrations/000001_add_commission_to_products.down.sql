-- File: db/migrations/000002_add_commission_to_products.up.sql
ALTER TABLE products
DROP COLUMN commission NUMERIC(10, 2) NULL; -- Or FLOAT NULL, etc. Adjust type as needed.