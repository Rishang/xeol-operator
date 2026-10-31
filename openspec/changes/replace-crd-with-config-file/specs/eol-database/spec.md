# Spec Delta

## MODIFIED Requirements

### Requirement: DB age
xeol's own age check SHALL be disabled (`XEOL_DB_VALIDATE_AGE=false`); the build time SHALL be exported as `xeol_db_built_timestamp_seconds` and as `db_built`/`db_age_seconds` in `/api/v1/status`, and alerted on (`XeolDatabaseStale`, 30 days by default).

#### Scenario: Old mounted DB
- **WHEN** a mounted DB is 40 days old
- **THEN** scans still run and the stale-DB alert fires
