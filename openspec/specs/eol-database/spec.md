# eol-database Specification

## Purpose
Keep a current xeol database, built by the operator from endoflife.date, available to every scan.

## Requirements

### Requirement: Build from endoflife.date
In `--db-source=online` (default), the operator SHALL build xeol's DB (`<db-dir>/1/xeol.db` + `metadata.json`) from endoflife.date's `products/full` at startup and every 24h, filling CPEs so distros match. In `--db-source=mounted` it SHALL only read a provided volume.

#### Scenario: Startup
- **WHEN** the operator starts online
- **THEN** it builds the DB, and the readiness probe fails until a DB is usable (`xeol db status` reports valid)

### Requirement: Failed rebuild keeps the old DB
A build SHALL be written to a temporary directory and swapped in only when complete. A failed rebuild SHALL log the error and keep the previous DB.

#### Scenario: endoflife.date down with a usable DB
- **WHEN** a rebuild fails and the existing DB is usable
- **THEN** it logs "EOL database rebuild failed; keeping the previous one" and tries again after 24h, not sooner

#### Scenario: No usable DB yet
- **WHEN** a build fails and there is no usable DB
- **THEN** it retries every 30s

### Requirement: DB age
xeol's own age check SHALL be disabled (`XEOL_DB_VALIDATE_AGE=false`); the build time SHALL be exported as `xeol_db_built_timestamp_seconds` and alerted on, and a DB older than 30 days sets `DBStale`.

#### Scenario: Old mounted DB
- **WHEN** a mounted DB is 40 days old
- **THEN** scans still run and the stale-DB alert fires
