-- Dropping the schemas takes their tables and indexes with them. Listing the
-- tables in reverse order here would be one more place to forget one, and
-- CASCADE resolves the references the two schemas hold on each other.
DROP SCHEMA IF EXISTS federation CASCADE;
DROP SCHEMA IF EXISTS identity CASCADE;
