# findings-export Specification

## Purpose
Expose current EOL findings and vulnerabilities as Prometheus metrics for alerting and as a JSON API for the Grafana dashboards, with accepted findings and vulnerabilities hidden.

## Requirements

### Requirement: Prometheus metrics
The operator SHALL export, from the in-memory inventory on each scrape:
- `xeol_eol_date_seconds` (value = EOL date) with labels `image`, `digest`, `product`, `cycle`, `package`, `version`, `url`;
- `xeol_image_workload` (1) with `digest`, `namespace`, `kind`, `name`, and `k8s_label_<key>` for each `workloadLabels` key;
- `xeol_db_built_timestamp_seconds`, `xeol_scan_duration_seconds` (histogram), `xeol_scan_errors_total{reason}`.

#### Scenario: Image stops running
- **WHEN** the last Pod running a digest is deleted
- **THEN** its series disappear on the next scrape

#### Scenario: Scan never started
- **WHEN** a scan fails before running (e.g. unreadable pull secret)
- **THEN** `xeol_scan_errors_total` increases and no duration sample is recorded

### Requirement: Bounded metric cardinality
EOL series SHALL be one per (digest, product, cycle), never per Pod, with no path, purl or timestamp labels.

#### Scenario: Same image in many Pods
- **WHEN** one digest runs in 50 Pods of a Deployment
- **THEN** each of its findings is one `xeol_eol_date_seconds` series, and the workload appears once in `xeol_image_workload`

### Requirement: JSON API
The operator SHALL serve read-only `GET /api/v1/{status,namespaces,workloads,findings}` on the metrics port, filtered by `namespace`, `kind` (comma-separated; `All` means no filter) and `within_days` (default 180). Each request SHALL be computed from one inventory snapshot. Non-leader replicas SHALL answer 503.

#### Scenario: Findings window
- **WHEN** `GET /api/v1/findings?within_days=0`
- **THEN** only findings already past EOL (or EOL with no date) are returned, soonest first

#### Scenario: Pod deleted mid-request
- **WHEN** a Pod is deleted while `/api/v1/workloads` is being computed
- **THEN** the response is consistent and the request does not fail

### Requirement: Ignore accepted findings
`ignoreProducts` rules SHALL hide findings from metrics, the JSON API and status without rescanning. A rule matches by product (endoflife.date slug or display name, case-insensitive), optionally narrowed by `cycles`, `namespaces` globs and a `selector` on workload labels (all must match). A finding SHALL be dropped from metrics only when the rule matches every workload running that digest.

#### Scenario: Namespace-scoped rule on a shared digest
- **WHEN** `{products: [perl], namespaces: [test]}` and a digest runs in `test` and `web`
- **THEN** the API hides perl for the `test` workload, and metrics keep the series

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

### Requirement: Ignore accepted vulnerabilities
`ignoreVulnerabilities` rules SHALL hide vulnerabilities from metrics, the API, status and the web page without rescanning. A rule matches by `ids` (vulnerability IDs) and/or `packages` (package names), case-insensitive, both must match when both are given, optionally narrowed by `namespaces` globs and a `selector` on workload labels. A vulnerability SHALL be dropped from metrics only when the rule applies to every workload running that digest.

#### Scenario: Ignore one CVE everywhere
- **WHEN** the config has `ignoreVulnerabilities: [{ids: [CVE-2024-1234]}]`
- **THEN** no `/api/v1/vulnerabilities` row has that ID, and the status, workload and metric counts leave it out

#### Scenario: Namespace-scoped rule on a shared digest
- **WHEN** `{packages: [libcrypto3], namespaces: [test]}` and a digest runs in `test` and `web`
- **THEN** the API hides libcrypto3 for the `test` workload only, and the metric still counts it

#### Scenario: Empty rule
- **WHEN** a rule has neither `ids` nor `packages`
- **THEN** the config file is rejected with an error naming the rule
