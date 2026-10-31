# Spec Delta

## MODIFIED Requirements

### Requirement: Build from endoflife.date
In `--db-source=online` (default), the operator SHALL build xeol's DB (`<db-dir>/1/xeol.db` + `metadata.json`) from endoflife.date's `products/full` at startup and every 24h, filling CPEs so distros match. In `--db-source=mounted` it SHALL only read a provided volume. With `--eol=false`, it SHALL neither build nor read the EOL DB, and readiness SHALL not wait for it.

#### Scenario: Startup
- **WHEN** the operator starts online
- **THEN** it builds the DB, and the readiness probe fails until a DB is usable (`xeol db status` reports valid)

#### Scenario: EOL scanning off
- **WHEN** the operator starts with `--eol=false --trivy`
- **THEN** it makes no request to endoflife.date and becomes Ready once the trivy DB is usable
