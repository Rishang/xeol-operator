# Design

## Context

See proposal.md for why. Today `Xeol.Scan` (`internal/scanner/xeol.go`) runs syft (`-o syft-json=sbom.json`) then xeol in one per-scan temp dir, under one in-flight OOM marker, one timeout and one `GOMEMLIMIT`/`GOMAXPROCS`. `eoldb.Manager` builds the EOL DB, swaps it in when complete, and gates readiness and the queue through `Wait`. `results.DB` stores one row per digest with findings as a JSON column.

Benchmark facts this design relies on (2026-10-29, `.local/research/trivy-vs-syft-efficiency.md`, trivy v0.76.0, syft v1.54.1, Grafana 12.2.1):
- syft writing syft-json and CycloneDX in one run costs no more than syft-json alone.
- `trivy sbom` on that CycloneDX: 0.17 s, ~150 MB peak.
- trivy reads syft's CycloneDX but not syft-json; with syft's SPDX it finds no OS packages.
- Without a fix-up trivy misses Alpine subpackages (29 vs 144 OS vulns); adding `aquasecurity:trivy:SrcName/SrcVersion/SrcRelease` properties brings it to 150.
- `trivy sbom` works with only the main DB (no Java DB): same 150 + 153 findings.

## Goals / Non-Goals

**Goals:**
- One image pull and one syft run per scan, whether trivy is on or off.
- Nothing trivy-specific leaks past `internal/scanner`, same rule as xeol.
- A trivy failure never costs EOL findings.

**Non-Goals:**
- Parallelizing xeol and trivy within a scan: both are short next to syft, and running them one after the other keeps peak memory at syft's.
- Reusing trivy's own image analyzers or server mode.

## Decisions

### D1. Feed trivy syft's CycloneDX, not `trivy image`
Add `-o cyclonedx-json=<work>/sbom.cdx.json` to the existing syft call when trivy is enabled, then `trivy sbom` on it.
- *Alternative: `trivy image`.* Second pull, +3 s / +310 MB per image, and a second view of the image that can disagree with xeol's. Rejected.
- *Alternative: trivy makes the SBOM, xeol reads it.* trivy has no binary classifiers, so xeol would lose Python/Node runtimes. Rejected.
- *Alternative: SPDX.* trivy finds no OS packages in syft's SPDX. Rejected.

### D2. Fix up the CycloneDX file in Go before trivy reads it
For each component whose purl is an OS type (`apk`, `deb`, `rpm`) and has an `upstream` qualifier, add properties `aquasecurity:trivy:SrcName` (upstream name, without any `@version` part), `SrcVersion` and `SrcRelease` (split from the component version: `-r<N>` for apk, last `-` for deb/rpm), and `aquasecurity:trivy:PkgType` if trivy needs it to group OS packages. Use `encoding/json` on generic maps so unknown fields pass through unchanged.
- These are trivy's own CycloneDX property names. They are an internal convention, not a public API, so the fixture test (D8) pins the behavior to the trivy version in the Dockerfile.
- *Alternative: send it upstream* (trivy reading `upstream=` from purls). Worth filing, but we can't wait for it.
- *Alternative: syft-json → a CycloneDX we generate ourselves.* More code, same property dependency. Rejected.

### D3. Scanner boundary: `Scan` returns a `Result`
`Scan(ctx, Target) (Result, error)` with `Result{Findings []Finding; Vulns []Vuln; VulnErr error}`. `Vuln` is flat: `ID, Package, Version, FixedVersion, Severity, Type, Title, URL` (URL = primary reference, e.g. the NVD or advisory page). The trivy step runs inside the same `Scan`, so it shares the per-scan dir, the OOM marker, the timeout and the GOMEMLIMIT/GOMAXPROCS env. `parseTrivy` dedupes (ID, Package, Version) like `parse` does for xeol.
- A trivy error does not fail `Scan`: it sets `VulnErr` (classified `trivy`), and the queue stores EOL findings and keeps the previous vulns (`Store.SetResult` gains vulns + a "vulns valid" flag).
- *Alternative: a separate scan queue for trivy.* It would need its own copy of the SBOM, or a second syft run. Rejected.

### D4. `internal/vulndb`: same lifecycle as `eoldb.Manager`
A `Manager` with `Start`, `Wait`, `Ready`, `Updated()` mirroring `eoldb.Manager`:
- Online: `trivy image --download-db-only --cache-dir <dir>/next --db-repository <repos> --skip-version-check --disable-telemetry --no-progress` (trivy pulls the DB as an OCI artifact), then swap `<dir>/next` → `<dir>/current` the same way `eoldb` swaps, keeping the old one on failure. Startup retries every 30s until the first success, then every 24h.
- Mounted: read `<dir>` as-is, expecting `db/trivy.db` and `db/metadata.json` (trivy's cache layout).
- `Updated()` reads `UpdatedAt` from `db/metadata.json` for `xeol_vuln_db_updated_timestamp_seconds`.
- The queue's `Wait` becomes "EOL DB ready, and trivy DB ready if enabled"; same for the readiness check.
- Flags: `--trivy`, `--trivy-bin` (default `trivy`), `--trivy-db-dir` (default `/var/cache/trivy`), `--trivy-db-repository` (default trivy's own list, so mirrors can be set). `--db-source` covers both DBs: one air-gap switch.
- *Alternative: let each `trivy sbom` download the DB.* Every scan becomes a network call and a possible 1.6 GB download. Rejected.

### D5. Scan invocation
`trivy sbom <work>/sbom.cdx.json --scanners vuln -f json -q --cache-dir <trivy-db-dir>/current --cache-backend memory --skip-db-update --skip-java-db-update --offline-scan --skip-version-check --disable-telemetry`, with `TRIVY_DISABLE_VEX_NOTICE=true` and the same `HOME/TMPDIR/XDG_*` sandboxing `run` already applies. Exit code non-zero = error (no `--exit-code`).

### D6. Storage and export
- `results` table: a `vulns TEXT NOT NULL` column in `CREATE TABLE` (JSON; `null` means never scanned with trivy, so a trivy failure keeps the previous rows). No migration: `results.db` is on the state emptyDir, and an upgrade is always a new pod with an empty volume.
- Collector: `xeol_vulnerabilities{digest,severity,fixable}` counts computed per scrape from the cached rows; zero counts are not emitted. Worst case is 5 × 2 = 10 series per digest.
- `GET /api/v1/vulnerabilities`: rows joined with workloads from the same inventory snapshot as `/findings`; 404 when trivy is off.
- Chart: `trivy.enabled`, `trivy.dbRepository`, a second `emptyDir` (`sizeLimit: 5Gi`) at `/var/cache/trivy`, an alert `XeolVulnDatabaseStale` (> 7 days), and the trivy dashboard (D9).

### D7. UI: the Trivy tab is the EOL report with a different data source
**Tab mechanics.** Tab links are `?tab=…` URLs that keep `namespace`, `kind` and `q`, with `hx-get="ui/report?…"`, so htmx swaps only `#report` and without htmx they are full reloads. The header's tab-dependent controls (hidden `tab` input, the window or "Fixed version" select) sit in `#tabctl`, which `/ui/report` re-renders with `hx-swap-oob`, so the filters form keeps submitting the tab shown. The URL bar is not updated, like the existing filters (`historyEnabled: false`). `ui.go` picks the `report` (EOL) or `trivy` template block from `tab`.

**Same UX, section for section** (spec: Trivy report mirrors the EOL report):

| EOL report | Trivy report | Reused as-is |
|---|---|---|
| Stamp "Updated …" | same | stamp markup |
| 7 tiles: past EOL, EOL within N days, images scanned/tracked, next EOL, EOL DB age, failed scans by reason (wide) | 6 tiles: workloads with CRITICAL, with HIGH, images scanned/vulnerable, fixable CRITICAL+HIGH, vuln DB age, count per severity (wide, info tips list images) | `.tile`, `.split`, `.cells`, `.tip`, `ageTone`, good/warn/bad tones |
| Runway: one mark per product cycle, click to filter (`pick`) | Package strip: a band per severity, one mark per vulnerable package in its worst severity's band, sized by count, click to filter (`pick`) | `button.mark` + `pick` handling; position is severity, not a date |
| Findings table, `sort` / `f_` / `v_` / `clear` popovers | Vulnerabilities table, same popovers; default sort severity, CVE | `filterTable` and the `thead` template block, parameterized by column set |
| "Upcoming window" select | "Fixed version" select (All / Fixable only) | header `label > select` |
| Workloads + Namespaces pair with `pill eol` / `pill soon` | same pair with CRITICAL / HIGH / fixable pills | `.pair`, `.pill`, `.zero`, `.wl` |
| Footer: EOL data source and age | Footer: trivy DB updated time and age | footer markup |

Severity colors reuse the UI's status tokens (`--bad` CRITICAL, `--eol` HIGH, `--warn` MEDIUM, `--muted` LOW/UNKNOWN), so no new palette.

**One data path.** Tiles read the `vulnerabilities` block of the status the API builds, the table reads the `/api/v1/vulnerabilities` rows, and the pair reads the extended workloads/namespaces rows, all from one inventory snapshot per request, as the EOL report does. The UI can't disagree with the API because it calls the same functions.

**Refactor first.** The findings table's sort/filter code becomes generic (`filterTable[R]`) and its header a `thead` template block; EOL uses them with byte-identical output (checked by diffing rendered pages) before the Trivy block is added. The Workloads/Namespaces pairs keep separate markup since their columns differ.

- *Alternative: separate `/trivy` page.* Duplicates the header, filters and standby handling. Rejected.
- *Alternative: client-side tabs (both reports rendered, toggled in CSS/JS).* Computes vulns on every EOL refresh and needs JS without htmx. Rejected.
- *Alternative: a simpler CVE-table-only tab.* Loses the summary-first scan the EOL tab gives (rollup, then detail). Rejected after review: "similar UX like EOL".

### D8. Tests
- Fixture test for the fix-up: real syft CycloneDX output (an Alpine image) in `testdata/`, asserting `SrcName=openssl` on `libcrypto3`.
- Fixture test for `parseTrivy` on real `trivy sbom -f json` output in `testdata/`, including a duplicate-path case.
- Optional integration test behind a build tag that runs the bundled trivy on the fixed-up fixture and asserts libcrypto3 findings, so a trivy bump that drops the property convention fails.
- envtest/queue test: trivy step error keeps EOL findings and previous vulns.

### D9. Grafana: a second dashboard on the same Infinity datasource
`dashboards/xeol-trivy.json` (uid `xeol-trivy`), added to the existing dashboard ConfigMap only when `trivy.enabled`. Every panel is an Infinity query against the operator's JSON API, with the same `datasource`, `namespace` and `kind` variables as `xeol.json`, plus a `severity` custom variable. Layout mirrors the EOL dashboard and the UI tab:

| Row | Panels | API |
|---|---|---|
| Stats | Workloads with CRITICAL · with HIGH · Images vulnerable · Fixable CRITICAL+HIGH · Vulnerability DB age · Failed vulnerability scans | `/api/v1/status` → `vulnerabilities`, `failed_scans_by_reason.trivy` |
| Tables | Vulnerabilities by namespace · by workload · Vulnerabilities by severity | `/api/v1/namespaces`, `/api/v1/workloads`, `/api/v1/status` |
| Detail | Vulnerabilities per workload: CVE (data link to `url`), title, severity (color), package, installed, fixed, image | `/api/v1/vulnerabilities?severity=${severity}` |
| Footer | Coverage: a text panel saying what trivy sees (OS and language packages from the SBOM) and what it doesn't | — |

Each dashboard links to the other (dashboard links, same time range and variables). Prometheus metrics stay for alerts only, as on the EOL dashboard.
- *Alternative: a row in `xeol.json`.* Panels show 404 errors when trivy is off, and the chart would have to edit JSON by flag. Rejected.
- *Alternative: Prometheus panels for counts.* A second datasource for the same numbers; the EOL dashboard is Infinity-only. Rejected.

### D10. Two switches: `--eol` (default on) and `--trivy` (default off)
Helm `eol.enabled` / `trivy.enabled` map to the flags. `cmd/main.go` exits if both are off; the chart fails rendering with the same message (`fail` in a helper).
- **Scanner:** `Xeol.Scan` runs syft with `syft-json` only when EOL is on and `cyclonedx-json` only when trivy is on, then xeol and/or trivy. With EOL off a trivy error is the scan's only error, recorded as `trivy` (previous vulns kept), same rule as D3.
- **DB managers:** only the enabled ones are started; the queue's `Wait` and the readiness check wait for the enabled ones.
- **Export:** the collector skips EOL series when EOL is off; `/api/v1/findings` answers 404; `/api/v1/status` adds `eol_enabled` (and `trivy_enabled`) so the UI and dashboards know what to show. `xeol_image_workload` and the workload/namespace endpoints always stay: both reports join on them.
- **Chart:** EOL alert rules and `xeol.json` render only with `eol.enabled`; trivy ones only with `trivy.enabled`.
- **UI:** tabs only when both are on; with one on, `/` is that report.
- *Alternative: a CR field.* Turning a scanner on or off changes DB downloads, volumes and image tools, all pod-level, like `--db-source`. Rejected.

## Risks / Trade-offs

- [trivy changes its CycloneDX property names] → pinned trivy version, fixture/integration test in D8; re-check on every trivy bump (added to the AGENTS.md bump checklist).
- [A trivy step that keeps failing reruns syft and xeol every hour (`RetryAfter`)] → accepted: the queue retries whole scans, and a persistent trivy failure is visible as `xeol_scan_errors_total{reason="trivy"}` (`XeolScanErrors`). Retrying only trivy would mean keeping the SBOM between scans.
- [Version split differs from trivy's own for some deb/rpm epochs] → fixture covers a Debian image; leave the epoch in `SrcVersion` the way the version string has it, and compare against `trivy image` on the test images during implementation.
- [1.6 GB DB per pod, downloaded daily] → off by default; `emptyDir` sizeLimit 5Gi (current + next is already 3.2 GB; leftover `.download-*` dirs are removed before each download, and a restart skips the download while the DB is less than a day old); mounted mode for air-gapped or bandwidth-limited clusters; `--trivy-db-repository` for a local mirror.
- [Image size +~150 MB for the trivy binary] → acceptable for an opt-in feature; one image keeps the chart simple.
- [Reading a bolt DB while it is being swapped] → scans open `<dir>/current` at start; the swap renames directories, so an open file stays valid until the scan ends (same as eoldb).
- [`trivy sbom` on a read-only mounted dir] → trivy may try to lock or write the cache dir; verify during implementation, and if it does, copy the mounted DB once to a writable `emptyDir` at startup.
- [Findings differ from `trivy image` on Go binaries (153 vs 160 on Grafana)] → documented as a known gap; scope is "trivy on syft's SBOM", not parity with trivy-operator.

## Migration Plan

- Off by default: upgrading changes nothing until `trivy.enabled=true`.
- Rollback: set `trivy.enabled=false`; vuln rows stay in `results.db` unused and are cleared with old rows.

## Open Questions

None. Titles and URLs go into the trivy dashboard's CVE table (D9), decided 2026-10-29.
