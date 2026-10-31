# Proposal

## Why

Teams using the operator for EOL tracking also want to know which running images have known vulnerabilities, and today that means running a second scanner (trivy-operator) that pulls and catalogs every image again. The operator already builds a full SBOM of each digest with syft for xeol; running trivy on that same SBOM adds vulnerability findings for ~0.2 s and ~150 MB per image (benchmark 2026-10-29, `.local/research/trivy-vs-syft-efficiency.md`), with no second image pull.

## What Changes

- **Two scan switches** in Helm: `eol.enabled` (default true, operator `--eol`) and `trivy.enabled` (default false, `--trivy`). syft runs when either is on; with EOL off the operator builds no EOL DB, runs no xeol, and ships no EOL metrics, alerts, dashboard or UI tab. Both off is rejected by the chart and at startup.
- **Opt-in vulnerability scan** (`--trivy`, Helm `trivy.enabled`, default off). When on, each scan's single syft run also writes a CycloneDX SBOM (no measurable cost), and the operator runs the bundled `trivy sbom` on it after xeol, the same way it runs xeol: shell out, parse `-f json`, behind the `internal/scanner` boundary.
- **SBOM fix-up for OS packages:** before trivy reads it, the operator adds trivy's source-package properties to each OS package from the purl's `upstream` qualifier. Without it trivy misses subpackages (Grafana 12.2.1 Alpine: 29 vulns vs 144 from `trivy image`; 150 with the fix-up).
- **Trivy vulnerability DB:** downloaded at startup and every 24h into its own dir (`--trivy-db-dir`) with the same swap-when-complete rule as the EOL DB; `--db-source=mounted` reads a provided volume instead. Only the main DB (~1.6 GB); trivy's Java DB is not needed for SBOM input (verified). Scans never update it or call out (`--skip-db-update --skip-java-db-update --offline-scan --skip-version-check --disable-telemetry`).
- **New metrics** (bounded: per digest × severity × fixable, never per CVE):
  - `xeol_vulnerabilities{digest, severity, fixable}` gauge, value = count of unique (CVE, package, version) findings. Needed for alerts without per-CVE cardinality; dashboards read the JSON API.
  - `xeol_vuln_db_updated_timestamp_seconds` gauge, from trivy's `metadata.json` `UpdatedAt`. Needed to alert on a stale vulnerability DB like `xeol_db_built_timestamp_seconds`.
- **New JSON API endpoint** `GET /api/v1/vulnerabilities` (per-CVE rows, filtered like `/findings`) for a Grafana table. Per-CVE detail lives here, not in metrics.
- **Trivy tab in the built-in UI** (`--ui`): with `--trivy`, the page gets two tabs, **EOL** (today's report, unchanged) and **Trivy**, built as the same report with vulnerability data: status tiles, clickable package marks (like the EOL runway), a Vulnerabilities table with the same sort and column-filter popovers, Workloads and Namespaces tables, and a DB footer. `/api/v1/status`, `/workloads` and `/namespaces` gain vulnerability counts so the tab and the API show the same numbers, as on the EOL tab. Without `--trivy` there are no tabs and the page is as today.
- **Trivy Grafana dashboard** (`dashboards/xeol-trivy.json`), shipped with `trivy.enabled`: stats, per-namespace and per-workload tables, and a CVE table with titles and reference links, all read from the operator's JSON API through the same Infinity datasource as the EOL dashboard.
- **Saved results** gain a `vulns` JSON column; a trivy failure keeps the EOL findings of that scan and is counted as `xeol_scan_errors_total{reason="trivy"}`.
- **Scope:** AGENTS.md "Out: vulnerability scanning" becomes "In, opt-in, through trivy on the syft SBOM".

No new CRD field. Both switches and DB settings are pod-level, so they are operator flags set by Helm, like `--db-source`.

## Non-goals

- Replacing syft with trivy for cataloging: trivy has no binary classifiers and misses runtimes installed outside a package manager (Python, Node), which are xeol's main findings.
- Running `trivy image` (second pull, +3 s / +310 MB per image) or any trivy scanner other than `vuln` (secrets, misconfig, licenses).
- Ignore rules or VEX for vulnerabilities (`ignoreProducts` stays EOL-only); add when a real case needs it.
- Severity thresholds as operator config: thresholds are PromQL, as with EOL dates.
- Trivy's Java DB, trivy server mode, or a shared trivy cache across pods.
- Vulnerabilities as an MCP tool: a follow-up once the API is settled.

## Capabilities

### New Capabilities
- `vulnerability-scanning`: optional trivy matching of each digest's SBOM, the SBOM fix-up, and the trivy vulnerability DB lifecycle.

### Modified Capabilities
- `image-scanning`: `--eol`/`--trivy` switches (at least one on); the syft step also writes CycloneDX when trivy is enabled, and only CycloneDX when EOL is off; saved results hold vulnerability rows; a new `trivy` error reason that does not discard EOL findings.
- `findings-export`: new metrics `xeol_vulnerabilities` and `xeol_vuln_db_updated_timestamp_seconds`, new endpoint `/api/v1/vulnerabilities`, vulnerability counts in `/api/v1/status`, `/workloads` and `/namespaces`.
- `eol-database`: no EOL DB is built or required with `--eol=false`.
- `web-ui`: EOL/Trivy tabs when `--trivy` is on; the Trivy report mirrors the EOL report section for section.

## Impact

- Code: `internal/scanner` (CycloneDX output, trivy exec, fix-up, parser + `testdata/` fixtures), new `internal/vulndb` (download, swap, readiness), `internal/results` (column migration), `internal/metrics/collector.go`, `internal/api` (endpoint, `ui.go` + `ui/page.html` Trivy tab), `cmd/main.go` flags.
- Image: trivy binary pinned in the Dockerfile with checksum (~+150 MB image size).
- Helm: `trivy.enabled`, a separate `emptyDir` for the trivy DB (sizeLimit 5Gi: the 1.6 GB DB plus the download being swapped in, and growth; 3Gi was too small for two copies), alert rule for a stale vuln DB, the trivy dashboard added to the dashboard ConfigMap.
- Network: the operator pulls the trivy DB from `mirror.gcr.io/aquasec/trivy-db:2` (fallback `ghcr.io/aquasecurity/trivy-db:2`) daily when enabled; air-gapped installs mount it.
- Memory: peak per scan unchanged (syft ~600–700 MB dominates; trivy step ~150 MB, runs after syft exits).
