-- A shop seeded with synthetic personal data, for the plugin evaluation.
-- Every value is generated; none belongs to a person.
CREATE TABLE customers (
  id serial PRIMARY KEY, full_name text NOT NULL, email text NOT NULL, phone text, ssn text,
  dob date, address text, card_number text, loyalty_tier text, created_at timestamptz DEFAULT now());
CREATE TABLE orders (
  id serial PRIMARY KEY, customer_id int REFERENCES customers(id), total numeric(10,2), status text, shipping_note text);
CREATE TABLE support_tickets (
  id serial PRIMARY KEY, customer_id int REFERENCES customers(id), subject text, body text, metadata jsonb);
CREATE TABLE employees (id serial PRIMARY KEY, name text, work_email text, salary numeric, iban text);
CREATE TABLE products (id serial PRIMARY KEY, sku text, name text, price numeric(10,2));
-- Column names that hide what they hold: the case name heuristics miss.
CREATE TABLE legacy_records (id serial PRIMARY KEY, ref text, contact text, note text);

-- Luhn check digit for a 15-digit prefix.
CREATE FUNCTION luhn(p text) RETURNS text LANGUAGE sql IMMUTABLE AS $$
  SELECT p || ((10 - (sum(CASE WHEN (length(p) - i) % 2 = 0 THEN (d*2) - CASE WHEN d*2 > 9 THEN 9 ELSE 0 END ELSE d END) % 10)) % 10)::text
  FROM (SELECT i, substr(p, i, 1)::int AS d FROM generate_series(1, length(p)) i) x $$;

INSERT INTO customers (full_name, email, phone, ssn, dob, address, card_number, loyalty_tier)
SELECT f || ' ' || l,
       lower(f) || '.' || lower(l) || i || '@' || (ARRAY['gmail.com','outlook.com','acme-mail.net','proton.me'])[1 + i % 4],
       '(' || (200 + i % 700) || ') ' || lpad((100 + i * 7 % 900)::text, 3, '0') || '-' || lpad((i * 37 % 10000)::text, 4, '0'),
       lpad((100 + i % 600)::text, 3, '0') || '-' || lpad((10 + i % 89)::text, 2, '0') || '-' || lpad((1000 + i * 13 % 8999)::text, 4, '0'),
       date '1960-01-01' + (i * 97 % 15000),
       (100 + i) || ' ' || (ARRAY['Maple','Oak','Cedar','Pine','Elm'])[1 + i % 5] || ' St, ' || (ARRAY['Springfield','Riverton','Lakeside','Fairview'])[1 + i % 4],
       luhn('4' || lpad((i * 7919 % 100000000000000)::text, 14, '0')),
       (ARRAY['bronze','silver','gold'])[1 + i % 3]
FROM generate_series(1, 200) i,
     LATERAL (SELECT (ARRAY['Ada','Grace','Alan','Linus','Barbara','Ken','Margaret','Dennis','Frances','Edsger'])[1 + i % 10] AS f,
                     (ARRAY['Lovelace','Hopper','Turing','Torvalds','Liskov','Thompson','Hamilton','Ritchie','Allen','Dijkstra'])[1 + (i / 10) % 10] AS l) n;

INSERT INTO orders (customer_id, total, status, shipping_note)
SELECT 1 + i % 200, (i * 13 % 50000) / 100.0, (ARRAY['pending','shipped','delivered','cancelled'])[1 + i % 4],
       CASE WHEN i % 5 = 0 THEN 'Leave at door. Call ' || c.phone || ' if no answer.'
            WHEN i % 7 = 0 THEN 'Gift - notify ' || c.email
            ELSE 'Standard delivery' END
FROM generate_series(1, 600) i JOIN customers c ON c.id = 1 + i % 200;

INSERT INTO support_tickets (customer_id, subject, body, metadata)
SELECT c.id, 'Issue #' || c.id,
       CASE c.id % 4
         WHEN 0 THEN 'Hi, I am ' || c.full_name || '. My card ' || c.card_number || ' was charged twice.'
         WHEN 1 THEN 'Please update my email to ' || c.email || ' and call me at ' || c.phone || '.'
         WHEN 2 THEN 'SSN on file is ' || c.ssn || ', it is wrong.'
         ELSE 'The app crashes when I open settings.' END,
       jsonb_build_object('contact_email', c.email, 'ip', '10.' || (c.id % 255) || '.' || (c.id * 7 % 255) || '.' || (c.id * 13 % 255), 'channel', 'web')
FROM customers c;

INSERT INTO employees (name, work_email, salary, iban)
SELECT 'Employee ' || i, 'emp' || i || '@acme.example', 50000 + i * 1000,
       'GB' || lpad((i * 7 % 97)::text, 2, '0') || 'NWBK' || lpad((601613 + i)::text, 6, '0') || lpad((31926819 + i)::text, 8, '0')
FROM generate_series(1, 20) i;

INSERT INTO products (sku, name, price)
SELECT 'SKU-' || i, 'Product ' || i, i * 3.5 FROM generate_series(1, 50) i;

INSERT INTO legacy_records (ref, contact, note)
SELECT replace(c.ssn, '-', ''), c.email, 'migrated from v1' FROM customers c WHERE c.id <= 50;

CREATE VIEW customer_contacts AS SELECT id, full_name, email, phone FROM customers;
