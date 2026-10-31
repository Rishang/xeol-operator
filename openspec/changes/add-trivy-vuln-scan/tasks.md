# Tasks

## 1. Image and fixtures

- [x] 1.1 Pin trivy v0.76.0 in the Dockerfile with checksum verification, like xeol/syft; verify `task docker-build` succeeds and `docker run --entrypoint trivy <image> --version` prints 0.76.0
- [x] 1.2 Save real fixtures under `internal/scanner/testdata/`: syft CycloneDX of `node:16-alpine` and `python:3.12-slim` (operator catalogers), and `trivy sbom -f json` output for each after fix-up; verify the files are committed-ready and `jq` parses them
- [x] 1.3 Add trivy to the AGENTS.md conventions (version pin, bump checklist: re-check CycloneDX property names and refresh trivy fixtures) and change "Out: vulnerability scanning" to opt-in via trivy on the syft SBOM; verify `rg -n trivy AGENTS.md` shows both

## 2. Scanner: CycloneDX fix-up and trivy step

- [x] 2.1 Implement the CycloneDX fix-up (design D2) with a fixture test asserting `libcrypto3` gets `SrcName=openssl` and non-OS components are unchanged; verify `go test ./internal/scanner/ -run FixUp`
- [x] 2.2 Implement `parseTrivy` returning flat `Vuln` rows deduped by (ID, Package, Version); verify `go test ./internal/scanner/ -run Trivy` against the fixtures, including a duplicate-path case
- [x] 2.3 Change `Scan` to return `Result{Findings, Vulns, VulnErr}`: syft writes `syft-json` only with EOL on and `cyclonedx-json` only with trivy on; xeol runs only with EOL on and `trivy sbom` (design D5 flags) only with trivy on; verify existing scanner tests pass, a fake trivy exiting 1 yields EOL findings plus `VulnErr` reason `trivy`, and an EOL-off scan never runs xeol
- [x] 2.4 Integration test behind a build tag: run the real trivy on the fixed-up Alpine fixture with a test DB dir and assert libcrypto3 findings; verify `go test -tags trivy ./internal/scanner/` passes locally with `TRIVY_DB_DIR` set
- [x] 2.5 Keep the per-scan dir removal on every return (it already deletes both SBOMs before the next image; deleting each SBOM earlier saved ~1 MB for a few seconds and was dropped in review), and sweep leftover `scan-*` dirs in the work dir at startup; verify a test that a pre-existing `scan-*` dir is removed by the sweep

## 3. Trivy DB manager

- [x] 3.1 Add `internal/vulndb.Manager` (online download into `next`, swap to `current`, 30s/24h retry, mounted mode, `Wait`, `Ready`, `Updated` from `metadata.json`), with unit tests using a fake trivy binary for success, failure-keeps-old, and mounted; verify `go test ./internal/vulndb/`
- [x] 3.2 Check whether `trivy sbom` works with a read-only `--cache-dir`; if not, copy a mounted DB to a writable dir at startup; verify by running the scan command from D5 against a `chmod -R a-w` copy of the DB
- [x] 3.3 Wire flags `--eol` (default true), `--trivy`, `--trivy-bin`, `--trivy-db-dir`, `--trivy-db-repository` in `cmd/main.go`; exit when both scan types are off; start only the enabled DB managers, and make queue `Wait` and readiness wait for exactly those (design D10); verify `go build ./...`, `go run ./cmd --help | rg -e trivy -e eol`, and `go run ./cmd --eol=false` exits with the both-off error

## 4. Storage, queue and export

- [x] 4.1 Add the `vulns` column to `results.db`'s `CREATE TABLE` (no migration: the file lives on the state emptyDir, and an operator upgrade is always a new pod with an empty volume); verify `go test ./internal/results/`
- [x] 4.2 Extend `Store.SetResult`/inventory to keep vulns per digest and keep previous vulns when `VulnErr` is set; verify an envtest/queue test for the "Trivy step fails" scenario
- [x] 4.3 Export `xeol_vulnerabilities{digest,severity,fixable}` and `xeol_vuln_db_updated_timestamp_seconds` from the collector, and count trivy errors as `xeol_scan_errors_total{reason="trivy"}`; verify `go test ./internal/metrics/` covers zero-count omission and series dropping when a digest stops running
- [x] 4.4 Add `GET /api/v1/vulnerabilities` (one row per workload, CVE and package; filters `namespace`, `kind`, `severity`; 404 when trivy is off; 503 on non-leaders); verify `go test ./internal/api/`
- [x] 4.5 Add the `vulnerabilities` block to `/api/v1/status` and `vuln_*` fields to `/api/v1/workloads` and `/api/v1/namespaces`, absent without `--trivy`; verify `go test ./internal/api/` checks both with and without trivy, and that without trivy no vulnerability key appears (status only gains `eol_enabled`/`trivy_enabled`)
- [x] 4.6 With `--eol=false`: no EOL series (no xeol step runs, so no image has findings; a flag change is always a new pod, so none are restored either), `/api/v1/findings` answers 404, `/api/v1/status` has `eol_enabled: false` (and `trivy_enabled`) with zero EOL counts; verify `go test ./internal/metrics/ ./internal/api/` for EOL on/off
- [x] 4.7 Update the AGENTS.md Metrics table and API line, and the `image-scanning` data model in the spec (vulns column); verify `openspec validate add-trivy-vuln-scan --strict`

## 5. Helm chart

- [x] 5.1 Add `eol.enabled` (default true) and `trivy.enabled` (default false) with a `fail` when both are off, `trivy.dbRepository`, the `/var/cache/trivy` emptyDir (sizeLimit 5Gi) and the flags to the Deployment; verify `task chart-check`, `helm template` with `trivy.enabled=true` shows the flags and volume, and with both off fails with the both-off message
- [x] 5.2 Add the `XeolVulnDatabaseStale` alert (> 7 days) to the PrometheusRule, rendered only with `trivy.enabled`; verify `task chart-test` and `promtool check rules` on the rendered rule
- [x] 5.3 Add `dashboards/xeol-trivy.json` (design D9: stats, tables, CVE table with title and reference data link, `severity` variable; all panels Infinity on `/api/v1/*`) to the dashboard ConfigMap only with `trivy.enabled`, plus links between the two dashboards; verify `helm template` with `trivy.enabled=false` has no `xeol-trivy.json` key, and with it on the JSON parses and every panel's datasource is the `${datasource}` Infinity variable
- [x] 5.4 Render the EOL alert rules and `xeol.json` only with `eol.enabled`; verify `helm template --set eol.enabled=false,trivy.enabled=true` has no EOL rules and no `xeol.json` key, and default values render exactly as before this change
- [x] 5.5 Document `trivy.enabled` in the chart README and the mounted trivy DB layout in `docs/air-gapped.md`; verify the documented `helm` command renders

## 6. Built-in UI: Trivy tab (same UX as EOL)

- [x] 6.1 Refactor: the findings table's sort/filter logic becomes generic (`filterTable`) and its header (sort + funnel popover) a `thead` template block, with EOL using them (the Workloads/Namespaces pairs keep their own markup: their columns differ); verify `go test ./internal/api/ -run UI` passes unchanged and the EOL page HTML is unchanged for the same fixture
- [x] 6.2 Add the `tab` parameter and EOL/Trivy tab links (tab links with `hx-get="ui/report"`; the hidden `tab` input and the window/fixable select are updated out of band from `/ui/report`; shown only when both scan types are on; with one on, `/` is that report); verify UI tests: no tabs without `--trivy`, `tab=trivy` without `--trivy` renders EOL, `--eol=false` renders Trivy at `/` without tabs, a tab switch keeps `namespace`/`kind`/`q`
- [x] 6.3 Add the Trivy tiles (workloads with CRITICAL, with HIGH, images scanned/vulnerable, fixable CRITICAL+HIGH, vuln DB age, per-severity wide tile with info tips) and the "not ready" state; verify a test that every tile equals the `/api/v1/status` `vulnerabilities` block for the same filters
- [x] 6.4 Add the package marks (worst severity color, size by count, `pick` to filter and clear); verify a test that picking `libcrypto3` leaves only its rows and marks the button `aria-pressed="true"`
- [x] 6.5 Add the Vulnerabilities table with the shared popovers (default sort severity, CVE) and the "Fixed version" select replacing the window select; verify tests for `v_severity=CRITICAL`, sort order, rows equal to `/api/v1/vulnerabilities`, and a `javascript:` reference URL rendering as text
- [x] 6.6 Add the Trivy Workloads/Namespaces pair (CRITICAL/HIGH/fixable pills, worst first) and the vuln DB footer; verify a test that counts equal `/api/v1/workloads` and `/api/v1/namespaces`
- [x] 6.7 Check both tabs side by side in a browser, light and dark, with and without htmx (block the CDN); verify sections line up, switching keeps filters, and add a Trivy tab screenshot to `docs/` next to the EOL one

## 7. End-to-end check

- [x] 7.1 On k3d (`--kube-context k3d-k3s-default`): `task deploy-dev -- -f test/dev-values.yaml --set trivy.enabled=true`; verify the pod becomes Ready after the trivy DB download, `xeol_vulnerabilities` has series for the `test` digests, EOL findings match `.claude/context/local-k3d-cluster.md`, and the UI's Trivy tab (port-forward 8080) shows the same rows as `/api/v1/vulnerabilities`
- [x] 7.2 Compare per-image OS vulnerability counts against `trivy image` for `node:16-alpine` and `python:3.12-slim`; verify the counts are equal or the differences are listed in design.md Risks
- [x] 7.3 Redeploy with `--set eol.enabled=false,trivy.enabled=true`; verify the pod becomes Ready without contacting endoflife.date (no EOL DB build in the logs), `xeol_eol_date_seconds` is absent, and the UI shows the Trivy report at `/`
- [x] 7.4 Measure scan time and peak memory with trivy on vs off for the test images; verify the trivy step adds < 1 s and does not raise peak memory above syft's

## Workflow follow-up

- Run `/opsx:archive` after review, which syncs the delta specs into `openspec/specs/`.
