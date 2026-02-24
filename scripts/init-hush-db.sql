-- DEV ONLY: hardcoded credentials for local development. Production deployments must use strong credentials via environment variables.
-- Create Hush API database and user (run on first postgres init).
-- Used when running both Synapse and Go backend; safe to run multiple times only on empty instance.
CREATE USER hush WITH PASSWORD 'hush';
CREATE DATABASE hush OWNER hush;
GRANT ALL PRIVILEGES ON DATABASE hush TO hush;
