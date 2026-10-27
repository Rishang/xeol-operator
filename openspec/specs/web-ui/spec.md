# web-ui Specification

## Purpose
TBD - created by archiving change add-ui-and-mcp. Update Purpose after archive.
## Requirements
### Requirement: Built-in HTML UI
When started with `--ui` (Helm `ui.enabled=true`), the operator SHALL serve a server-rendered HTML page at `/` on the metrics port, showing the JSON API's data: status (images, workloads, workloads EOL / EOL soon, next EOL, DB age, failed scans), per-namespace counts, workloads with EOL counts, and findings with days left and an endoflife.date link. The only external resource SHALL be htmx, loaded by the browser from a pinned CDN URL with Subresource Integrity; without it the page SHALL still render and filter by full reload. All cluster data SHALL be HTML-escaped on the server.

#### Scenario: Open the UI
- **WHEN** `ui.enabled=true` and a user port-forwards the metrics port and opens `http://localhost:8080/`
- **THEN** the page shows the same numbers as `/api/v1/status` and the same rows as `/api/v1/workloads` and `/api/v1/findings`

#### Scenario: UI disabled
- **WHEN** the operator runs without `--ui`
- **THEN** `GET /` returns 404 and `/api/v1/*` is unchanged

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
