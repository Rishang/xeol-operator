# web-ui Specification

## Purpose
A built-in, read-only web page for clusters without Grafana: the same EOL findings and vulnerabilities as the JSON API, with filters, served by the operator itself.

## Requirements

### Requirement: Built-in HTML UI
When started with `--ui` (Helm `ui.enabled=true`), the operator SHALL serve a server-rendered HTML page at `/` on the metrics port, showing the JSON API's data: status (images, workloads, workloads EOL / EOL soon, next EOL, DB age, failed scans), per-namespace counts, workloads with EOL counts, and findings with days left and an endoflife.date link.

#### Scenario: Open the UI
- **WHEN** `ui.enabled=true` and a user port-forwards the metrics port and opens `http://localhost:8080/`
- **THEN** the page shows the same numbers as `/api/v1/status` and the same rows as `/api/v1/workloads` and `/api/v1/findings`

#### Scenario: UI disabled
- **WHEN** the operator runs without `--ui`
- **THEN** `GET /` returns 404 and `/api/v1/*` is unchanged

### Requirement: Page resources and escaping
The only external resource SHALL be htmx, loaded by the browser from a pinned CDN URL with Subresource Integrity; without it the page SHALL still render and filter by full reload. All cluster data SHALL be HTML-escaped on the server.

#### Scenario: CDN blocked
- **WHEN** the browser cannot load htmx
- **THEN** the page renders, and changing a filter reloads the page with the filtered data

### Requirement: Filters
The UI SHALL filter by namespace, kind and the "EOL soon" window (days) as query parameters `namespace`, `kind` and `within_days`. With htmx, a filter change or the Refresh button SHALL replace only the report with `GET /ui/report`. The findings table SHALL sort by any column (click the name; again to reverse) and filter each column through a popover with a "contains" text and a checklist of the column's values (`f_<col>`, `v_<col>`, `clear=<col>`).

#### Scenario: Filter by namespace
- **WHEN** the user selects namespace `test`
- **THEN** tiles and tables show only workloads in `test`

### Requirement: Standby replica
On a non-leader replica, the UI SHALL render a standby notice (HTTP 200, so htmx swaps it in) instead of empty tables.

#### Scenario: Port-forward hits a standby
- **WHEN** the page or `/ui/report` is requested from a non-leader
- **THEN** it says to reload or port-forward the leader pod

#### Scenario: Hostile values
- **WHEN** an image reference contains `<script>` or a finding URL is `javascript:`
- **THEN** both render inert

### Requirement: EOL and Trivy tabs
With `--ui`, `--eol` and `--trivy`, the page SHALL show two tabs, **EOL** (default; the existing report) and **Trivy**, selected by the query parameter `tab` (`eol`, `trivy`). With only one scan type on, the page SHALL show that report without tabs. A tab change SHALL keep the namespace, kind and row filters, swap only the report through `GET /ui/report` with htmx, and work by full reload without it. An unknown or disabled `tab` value SHALL render the enabled report.

#### Scenario: Switch to Trivy
- **WHEN** namespace `test` is selected and the user clicks the Trivy tab
- **THEN** the report is requested with `tab=trivy&namespace=test` and the Trivy report for `test` replaces the EOL report

#### Scenario: Trivy disabled
- **WHEN** the operator runs with `--ui` but without `--trivy`
- **THEN** the page has no tabs and looks as before this change

#### Scenario: EOL disabled
- **WHEN** the operator runs with `--ui --eol=false --trivy`
- **THEN** `/` shows the Trivy report without tabs, and the brand subtitle names vulnerabilities instead of end-of-life software

### Requirement: Trivy report mirrors the EOL report
The Trivy report SHALL use the EOL report's layout, components and wording style, in the same order: update stamp, a row of stat tiles, a strip of clickable marks, the Vulnerabilities table, the Workloads and Namespaces tables side by side, and a footer naming the database and its age. Standby and empty-state behavior SHALL match the EOL report.

#### Scenario: Same layout
- **WHEN** a user switches from EOL to Trivy
- **THEN** each section is in the same place, with the same tile, table, pill and popover styles

#### Scenario: Standby replica
- **WHEN** the Trivy report is requested from a non-leader
- **THEN** it shows the same standby notice as the EOL report

### Requirement: Trivy tiles
The Trivy tiles SHALL be, in order: workloads with CRITICAL (red if any, else green), workloads with HIGH (amber if any, else green), images scanned and images with a CRITICAL or HIGH vulnerability, CRITICAL and HIGH vulnerabilities with a fixed version, vulnerability DB age (toned like the EOL DB age), and a wide tile with counts per severity whose info tips list the affected images. The numbers SHALL equal the `vulnerabilities` block of `/api/v1/status` for the same filters.

#### Scenario: Tiles match the API
- **WHEN** the Trivy tab is opened with namespace `test`
- **THEN** every tile number equals the matching field of `GET /api/v1/status?namespace=test`'s `vulnerabilities` block

#### Scenario: Trivy DB not ready
- **WHEN** the trivy DB has not been downloaded yet
- **THEN** the DB age tile says "not ready" and the table says the vulnerability database is not ready, instead of showing empty results

### Requirement: Package marks
Like the EOL runway, the Trivy report SHALL show a strip with one tinted band per severity (CRITICAL left to UNKNOWN right, each labelled with its package count) and one mark per vulnerable package in the band of its worst severity, colored by that severity and sized by its vulnerability count. Clicking a mark SHALL filter the Vulnerabilities table to that package; clicking it again SHALL clear the filter.

#### Scenario: Pick a package
- **WHEN** the user clicks the `libcrypto3` mark
- **THEN** the table shows only `libcrypto3` rows and the mark shows as pressed

### Requirement: Vulnerabilities table
The table SHALL have one row per (workload, CVE, package) with columns in the order an admin reads them: severity, CVE (linked to its reference), package, installed version, fixed version, namespace, kind, name, image, package type and scanned time, the same order as the Grafana table; sorted by severity then CVE by default. Every column SHALL sort and filter through the same popover as the findings table (`sort`, `f_<col>`, `v_<col>`, `clear=<col>`).

#### Scenario: Filter by severity
- **WHEN** the user ticks only `CRITICAL` in the Severity column's popover and presses OK
- **THEN** only CRITICAL rows remain and the URL carries `v_severity=CRITICAL`

#### Scenario: Same rows as the API
- **WHEN** the Trivy tab is opened with namespace `test` and no column filters
- **THEN** its rows are the rows of `GET /api/v1/vulnerabilities?namespace=test`, one per workload

### Requirement: Fixed version select
On the Trivy tab, the header's "Upcoming window" select SHALL be replaced by a "Fixed version" select (All, Fixable only).

#### Scenario: Fixable only
- **WHEN** the user picks "Fixable only"
- **THEN** the table keeps only rows with a fixed version

### Requirement: Vulnerabilities table row cap
The Vulnerabilities table SHALL render at most 1,000 rows, in its current sort order, and say how many rows the filters matched; `/api/v1/vulnerabilities` SHALL return every row.

#### Scenario: Large cluster
- **WHEN** the filters match 17,000 rows
- **THEN** the table shows the first 1,000 and a note saying 1,000 of 17,000, with a link to `/api/v1/vulnerabilities`

### Requirement: Trivy workloads and namespaces
The Workloads table SHALL list every scanned workload worst first with CRITICAL, HIGH and fixable counts as pills; the Namespaces table SHALL show affected workloads out of scanned, and CRITICAL and HIGH counts. Counts SHALL equal `/api/v1/workloads` for the same filters.

#### Scenario: Worst first
- **WHEN** one workload has 2 CRITICAL and another has 0 CRITICAL and 9 HIGH
- **THEN** the workload with CRITICAL findings is listed first

### Requirement: Trivy escaping
All Trivy values SHALL be HTML-escaped on the server, and reference URLs that are not http(s) SHALL render as plain text.

#### Scenario: Hostile reference URL
- **WHEN** a vulnerability's reference URL is `javascript:alert(1)`
- **THEN** it renders as inert text, not a link
