# Spec Delta

## ADDED Requirements

### Requirement: Vulnerability metrics
With `--trivy`, the operator SHALL export, from the in-memory inventory on each scrape, `xeol_vulnerabilities` (value = count of unique (CVE, package, version) rows) with labels `digest`, `severity` (`CRITICAL`, `HIGH`, `MEDIUM`, `LOW`, `UNKNOWN`) and `fixable` (`true` if a fixed version exists), and `xeol_vuln_db_updated_timestamp_seconds`. No metric SHALL carry a CVE ID, package or path label.

#### Scenario: Image stops running
- **WHEN** the last Pod running a digest is deleted
- **THEN** its `xeol_vulnerabilities` series disappear on the next scrape

#### Scenario: Zero counts are omitted
- **WHEN** a digest has no LOW vulnerabilities
- **THEN** no `xeol_vulnerabilities{severity="LOW"}` series exists for it

### Requirement: Vulnerabilities API
With `--trivy`, the operator SHALL serve read-only `GET /api/v1/vulnerabilities` on the metrics port with one row per (workload, CVE, package) like `/api/v1/findings`, filtered by `namespace`, `kind` and `severity` (comma-separated; `All` means no filter), sorted by severity then CVE. Without `--trivy` it SHALL return 404. Non-leader replicas SHALL answer 503.

#### Scenario: Row fields
- **WHEN** `GET /api/v1/vulnerabilities` returns a row
- **THEN** it has namespace, kind, name, CVE, title, severity, package, installed and fixed version, image, package type, reference URL, digest and scanned time

#### Scenario: Critical only
- **WHEN** `GET /api/v1/vulnerabilities?severity=CRITICAL&namespace=test`
- **THEN** only CRITICAL rows for workloads in namespace `test` are returned

#### Scenario: Trivy disabled
- **WHEN** the operator runs without `--trivy` and `/api/v1/vulnerabilities` is requested
- **THEN** it answers 404

### Requirement: Vulnerability summaries in status, workloads and namespaces
With `--trivy`, `/api/v1/status` SHALL add a `vulnerabilities` object (`by_severity`, `workloads_critical`, `workloads_high`, `images_vulnerable`, `fixable_critical_high`, `db_updated`, `db_age_seconds`), and each `/api/v1/workloads` and `/api/v1/namespaces` row SHALL add `vuln_critical`, `vuln_high` and `vuln_fixable` (namespaces also `workloads_vulnerable`). Without `--trivy` these fields SHALL be absent.

#### Scenario: Status with trivy
- **WHEN** `GET /api/v1/status?namespace=test` with `--trivy`
- **THEN** the response has a `vulnerabilities` object counted over workloads in `test` only

#### Scenario: Vulnerable means CRITICAL or HIGH
- **WHEN** an image has only MEDIUM and LOW vulnerabilities
- **THEN** it counts in `by_severity` but not in `images_vulnerable`, and its workloads don't count in a namespace's `workloads_vulnerable`

#### Scenario: Status without trivy
- **WHEN** `GET /api/v1/status` without `--trivy`
- **THEN** the response has no `vulnerabilities` key and `trivy_enabled: false`; the only other addition is `eol_enabled`

### Requirement: Trivy Grafana dashboard
When `trivy.enabled` and the Grafana dashboard are both on, the chart SHALL ship a trivy dashboard for vulnerabilities whose panels read only the operator's JSON API through the same Infinity datasource and `namespace`/`kind` variables as the EOL dashboard. Its CVE table SHALL show the title and link each CVE to its reference URL. When both dashboards are installed, the EOL dashboard SHALL be unchanged except for a link to the trivy one.

#### Scenario: Trivy off
- **WHEN** the chart is installed with the dashboard on and `trivy.enabled=false`
- **THEN** only the EOL dashboard is installed and no panel queries `/api/v1/vulnerabilities`

#### Scenario: CVE link
- **WHEN** a user clicks a CVE in the trivy dashboard's table
- **THEN** its reference URL opens

### Requirement: EOL exports follow the EOL switch
With `--eol=false`, the operator SHALL not export `xeol_eol_date_seconds` or `xeol_db_built_timestamp_seconds`, `/api/v1/findings` SHALL answer 404, `/api/v1/status` SHALL report `eol_enabled: false` with zero EOL counts, and the chart SHALL not install the EOL alert rules or the EOL dashboard. `xeol_image_workload` and the workload/namespace endpoints SHALL stay, since vulnerability data joins on them.

#### Scenario: Trivy-only install
- **WHEN** the chart is installed with `eol.enabled=false` and `trivy.enabled=true`
- **THEN** the trivy dashboard is installed and the EOL one is not, the alert rules are `XeolVulnDatabaseStale` and `XeolScanErrors` (failed scans of either type) without the EOL rules, and `xeol_image_workload` is still exported
