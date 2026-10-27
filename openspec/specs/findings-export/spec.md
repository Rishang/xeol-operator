# findings-export Specification

## Purpose
Expose current EOL findings as Prometheus metrics for alerting and as a JSON API for the Grafana dashboard, with accepted findings hidden.

## Requirements

### Requirement: Prometheus metrics
The operator SHALL export, from the in-memory inventory on each scrape:
- `xeol_eol_date_seconds` (value = EOL date) with labels `image`, `digest`, `product`, `cycle`, `package`, `version`, `url`;
- `xeol_image_workload` (1) with `digest`, `namespace`, `kind`, `name`, and `k8s_label_<key>` for each `workloadLabels` key;
- `xeol_db_built_timestamp_seconds`, `xeol_scan_duration_seconds` (histogram), `xeol_scan_errors_total{reason}`.

Series SHALL be one per (digest, product, cycle), never per Pod, with no path, purl or timestamp labels.

#### Scenario: Image stops running
- **WHEN** the last Pod running a digest is deleted
- **THEN** its series disappear on the next scrape

#### Scenario: Scan never started
- **WHEN** a scan fails before running (e.g. unreadable pull secret)
- **THEN** `xeol_scan_errors_total` increases and no duration sample is recorded

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
