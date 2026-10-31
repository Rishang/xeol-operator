# AGENTS.md

## Goal

Kubernetes operator that runs [xeol](https://github.com/xeol-io/xeol) (end-of-life package scanner) against container images in a cluster and exposes the results as Prometheus metrics, so EOL findings can be graphed and alerted on in Grafana.

```
cluster workloads ──> operator collects images ──> xeol scan ──> /metrics ──> Prometheus ──> Grafana
```

## Scope

In:
- Discover images from running Pods (all namespaces, or those selected in the config file).
- Scan each unique image digest with xeol, cache results by digest, rescan on an interval.
- Export findings on a Prometheus `/metrics` endpoint, plus alert rules.
- Optional vulnerability scan (`trivy.enabled`, off by default): trivy matches the CycloneDX SBOM from the same syft run. EOL scanning can be switched off (`eol.enabled`); at least one stays on.
- Ship a Helm chart (`helm/xeol-operator`) with an optional ServiceMonitor, PrometheusRule and Grafana dashboard.
- For clusters without Grafana: an optional built-in HTML UI (`ui.enabled`) and MCP over HTTP (`mcp.enabled`).

Out (until asked for):
- Vulnerability scanning beyond `trivy sbom` on the syft SBOM: no `trivy image` scans (`trivy image --download-db-only` only fetches the DB, `internal/vulndb`), no secret/misconfig/license scanners, no VEX. Accepted vulnerabilities are hidden at export by `ignoreVulnerabilities`, not passed to trivy.
- An external database, or pushing to Grafana. Grafana reads Prometheus and the operator's JSON API.
- Auth, Ingress or write actions for the built-in UI/MCP.
- Webhooks or admission control that block EOL images.

## Stack

- Go, [kubebuilder](https://book.kubebuilder.io/) / controller-runtime layout.
- syft, xeol and trivy run as binaries (all bundled in the operator image): syft writes the SBOM once (syft-json for xeol, CycloneDX for trivy), xeol and `trivy sbom` match it with JSON output. Shell out, parse the JSON. Don't vendor them as libraries unless the CLIs can't do the job.
- Metrics through controller-runtime's built-in Prometheus registry (`sigs.k8s.io/controller-runtime/pkg/metrics`). No separate exporter process.
- Taskfile (`Taskfile.yml`) is the task runner. It replaces kubebuilder's Makefile; add targets there rather than documenting raw commands.

## Decisions (2026-10-26)

Recorded in `.local/plan/xeol-operator-mvp.plan.html`.
- **Scanner:** xeol and trivy behind a boundary. `internal/scanner` returns a `Result` of flat `Finding` and `Vuln` rows; nothing xeol- or trivy-specific leaks past it.
- **EOL data:** the operator builds xeol's DB itself from [endoflife.date](https://endoflife.date/api/v1/products/full) (xeol's own DB stopped updating 2026-09-02 and never matched distros). xeol is only the matcher. Never contact `data.xeol.io`.
- **Export:** Prometheus `/metrics` for alerts, plus a read-only JSON API (`/api/v1/{status,namespaces,workloads,findings,vulnerabilities}`, `internal/api`) on the metrics port for the Grafana dashboard via the Infinity datasource (D5, 2026-10-27). Both read the in-memory inventory. No Loki, no external database.
- **UI and MCP (2026-10-27):** `--ui` serves a server-rendered page (`internal/api/ui/page.html`, Go `html/template`) at `/`; htmx 2.0.4 from jsDelivr (pinned, SRI) swaps in `/ui/report` on filter changes (and the Refresh button), and without it the filters form does a plain reload; `--mcp` serves `status`, `namespaces`, `workloads` and `findings` (not `vulnerabilities` yet) as read-only MCP tools at `/mcp` (official Go SDK, stateless Streamable HTTP, cross-origin protected). Same port, no auth, off by default; non-leaders answer 503.
- **Lookahead:** always scan with `-l 10y`; export the EOL date as the metric value. "How close counts" is a PromQL threshold, not operator config.
- **Stale DB:** disable xeol's age check (`XEOL_DB_VALIDATE_AGE=false`), export `xeol_db_built_timestamp_seconds`, alert on age.

## Configuration (2026-10-31: replaced the XeolScanner CRD)

One YAML file, `--config` (default `/etc/xeol-operator/config.yaml`), parsed strictly into `internal/config.Config` (Go struct = the schema; unknown fields and bad values are errors). The chart renders it from `scanner.*` values into the ConfigMap `<release>-config`, mounted without `subPath`; `config.Watcher` re-reads it every 5 s and applies a changed file without a restart. A bad file at startup exits; a bad edit later keeps the previous config, is logged, and shows as `config_error` in `/api/v1/status`. Missing file = defaults. No CRD, no CR status: progress and health are `/api/v1/status` (incl. `last_sweep`), metrics and readiness.

```yaml
namespaceSelector: {}        # empty = all namespaces
excludeNamespaces: [kube-*]  # globs
excludeImages: []            # globs on image ref
interval: 24h                # rescan each image digest this often
workloadKinds: [Deployment, StatefulSet, DaemonSet, Job, CronJob]  # default when absent; [] = every kind
workloadLabels: [team]       # copied onto metrics as k8s_label_<name>
ignoreProducts:              # hide accepted findings at export time (no rescan)
  - products: [perl]         # endoflife.date slugs; optional cycles, namespaces, selector (workload labels)
    namespaces: [test]
ignoreVulnerabilities:       # same scoping; a rule needs ids and/or packages (both: that CVE in that package)
  - ids: [CVE-2024-1234]
```

Globs (`excludeNamespaces`, `excludeImages`, `ignoreProducts[].namespaces`, `ignoreVulnerabilities[].namespaces`): only `*` (anything, `/` included) and `?` (one character) are special; every string is a valid glob.

Pod-level settings are operator flags set by Helm, not config file fields (they need a restart anyway): `--scan-workers` (default 1), `--scan-memory-limit` (GOMEMLIMIT for xeol), `--scan-max-procs` (GOMAXPROCS), `--db-source` (`online` | `mounted`), `--db-dir`, `--state-dir`, `--eol` (default true), `--trivy` (default false), `--trivy-bin`, `--trivy-db-dir`, `--trivy-db-repository`.

Add fields only when a real use case needs them.

## Metrics

Prefix `xeol_`. Keep label cardinality bounded: one series per (digest, product, cycle), not per Pod. No path, purl or timestamp labels.

| Metric | Type | Labels / value |
|---|---|---|
| `xeol_eol_date_seconds` | gauge, value = EOL date (unix) | `image`, `digest`, `product`, `cycle`, `package`, `version`, `url` (endoflife.date page) |
| `xeol_image_workload` | gauge (1) | `digest`, `namespace`, `kind`, `name`, `k8s_label_*` (join in Grafana) |
| `xeol_db_built_timestamp_seconds` | gauge | — |
| `xeol_vulnerabilities` | gauge, value = count (`--trivy`) | `digest`, `severity`, `fixable` (never per CVE; CVEs are in `/api/v1/vulnerabilities`) |
| `xeol_vuln_db_updated_timestamp_seconds` | gauge (`--trivy`) | — |
| `xeol_scan_duration_seconds` | histogram | — |
| `xeol_scan_errors_total` | counter | `reason` |

PromQL: already EOL `xeol_eol_date_seconds < time()`; days left `(xeol_eol_date_seconds - time()) / 86400`.

`scanner.parse` drops repeat matches of the same (product, cycle, package, version) found at several paths, so the collector and JSON API emit each once.

A custom `prometheus.Collector` reads the in-memory cache on each scrape, so images that stop running drop their series with no deletion.

## Architecture

```
            watch Pods
 kube-apiserver ──────────> PodReconciler
                                │  containerStatuses[].imageID  (repo@sha256:...)
                                │  ownerRefs -> top workload (Pod→RS→Deployment, ...)
                                ▼
                         Inventory  digest -> {image ref, workloads, pullSecrets}
                                │  new digest, or lastScan older than interval
                                ▼
                         Scan queue (workqueue, keyed by digest, N workers)
                                │  exec: syft scan registry:<repo>@<digest> -o syft-json=sbom.json [-o cyclonedx-json=sbom.cdx.json]
                                │        xeol sbom:sbom.json -o json -q -l 10y            (--eol, default on)
                                │        trivy sbom sbom.cdx.json --scanners vuln -f json  (--trivy, default off)
                                ▼
                         Result cache  digest -> {findings, vulns, scannedAt, err}
                                │
                                ▼
           Prometheus  <──  /metrics (Collector reads Inventory + cache)  ──> alerts
           Grafana     <──  /api/v1/* JSON (Infinity datasource, same port)

 DB builder (ticker, daily): endoflife.date → xeol.db → shared cache dir on emptyDir
 Vuln DB (--trivy, daily):    trivy --download-db-only → <trivy-db-dir>/current on emptyDir
```

- **Image identity:** scan by the digest in `pod.status.containerStatuses[].imageID`, not the tag, so results match what actually runs. Dedupe by digest across the cluster.
- **Scan execution:** in-process `exec`, one worker by default, two steps per image: the bundled syft catalogs the image into an SBOM (xeol's cataloger selection, `catalogers` in `internal/scanner/xeol.go`), then xeol matches `sbom:<file>`. xeol v0.10.8's built-in syft v1.10 needs over twice the memory (grafana: 1.6 GB vs 0.78 GB, same findings). The operator only bounds the cost (memory limit, `GOMEMLIMIT`, `GOMAXPROCS=2`, timeout), never reimplements cataloging. An image that OOM-kills the pod leaves an in-flight marker on the state emptyDir; it is skipped for 24h as `reason="oom"` instead of crash-looping.
- **Registry auth:** per scan, a temp `config.json` from the Pod's (and its ServiceAccount's) `imagePullSecrets` in an `emptyDir` as `DOCKER_CONFIG`, deleted after.
- **EOL DB (`--db-source=online`):** `internal/eoldb` builds xeol's DB from endoflife.date at startup and daily; readiness waits for the first good DB. Scans run with `XEOL_DB_AUTO_UPDATE=false`. A build swaps in only when complete: a failed rebuild keeps the old DB, logs it, and retries next day (every 30s if there is no usable DB). `--db-source=mounted` reads a read-only volume instead (air-gapped, `ui/src/content/docs/air-gapped.mdx`).
- **Rescans:** each digest `interval` after its own last scan; a DB rebuild triggers none (decided 2026-10-27, avoids bunching scans). Results hold the EOL date and "already EOL" is computed at query time, so only endoflife.date data changes wait for a rescan (≤ ~2× interval).
- **State:** inventory in memory; each digest's last scan also in SQLite (`<state-dir>/results.db`, `internal/results`) on the state emptyDir, so after a container restart (e.g. OOM) a re-seen digest is restored and rescanned only if overdue. A new pod starts fresh.

## xeol CLI facts (verified against v0.10.8)

- Scan a registry image without a container runtime: `xeol registry:<repo>@sha256:<digest>`.
- Output: `-o json`. Each `matches[]` item has `Cycle` (`ProductName`, `ReleaseCycle`, `Eol`, `LatestRelease`, ...) and `artifact` (`name`, `version`, `type`, `purl`, `locations`). The top-level `Package` field is present but empty; read `artifact`. `source.target` has `manifestDigest`, `repoDigests`, `os`, `architecture`. `distro` has `name`/`version`.
- Exit code 1 on scan error (bad ref, auth, pull failure), with the message on stderr. Exit 0 with or without findings. Don't use `--fail-on-eol-found`.
- `--lookahead`: always pass it (we use `10y`); the documented defaults are wrong and `none` is rejected.
- Env: `XEOL_DB_CACHE_DIR`, `XEOL_DB_AUTO_UPDATE=false`, `XEOL_CHECK_FOR_APP_UPDATE=false`, `DOCKER_CONFIG`. Config can also come from `.xeol.yaml`.
- Never pass `--api-key`: it uploads scan results to xeol.io.
- DB format: `<cache>/1/xeol.db` (SQLite: `products`, `cycles`, `purls`, `cpes`, `vulns`, `id`) + `<cache>/1/metadata.json` (`version`, `built`, `checksum`). xeol's upstream DB leaves `cpes` empty, so distros never match; a DB built from endoflife.date fills `cpes` and matches Debian/Alpine (verified).
- endoflife.date `eolFrom` is the end of the last tracked support phase (Debian 12 → 2028 LTS end). Releases with no date and `isEol=false` produce `Eol: "false"` in xeol output.

## Spec-driven workflow (OpenSpec)

Changes go through [OpenSpec](https://github.com/Fission-AI/OpenSpec): `/opsx:propose` → review → `/opsx:apply` → `/opsx:archive`.
- `openspec/specs/` holds the current agreed behaviour; `openspec/changes/` holds in-flight proposals.
- Non-trivial features start as a change proposal, not as code.
- `openspec validate` must pass before applying a change.

## Local context

`.local/` is gitignored. If `.local/context/*.md` exists, read every file there before starting work; it holds local notes and references (e.g. trivy-operator as a design reference). Shared context (e.g. the local k3d test cluster) is indexed in `.claude/context/index.md`.

## Conventions

- Pin the xeol and syft versions in the Dockerfile. On an xeol bump, re-check the "xeol CLI facts" section, the `catalogers` list (xeol's `getProviderConfig`) and refresh `testdata/` fixtures. xeol v0.10.8 reads syft-json schema 16.x SBOMs (verified with syft v1.55.0).
- Pin trivy in the Dockerfile too. On a trivy bump, re-check the CycloneDX fix-up property names (`aquasecurity:trivy:SrcName/SrcVersion/SrcRelease`, `internal/scanner/trivy.go`), run `task test-trivy TRIVY_DB_DIR=<dir with db/trivy.db>`, and refresh the trivy `testdata/` fixtures. Scans never update the trivy DB or call out (`--skip-db-update --skip-java-db-update --offline-scan --skip-version-check --disable-telemetry`); only the main DB is used, not the Java DB.
- Reconcile is idempotent; scans run off the reconcile loop through a bounded worker queue keyed by digest.
- Private registries: reuse the Pod's `imagePullSecrets`. Never log credentials.
- Run as non-root, read-only root filesystem; xeol's DB cache goes on an `emptyDir` (or PVC if configured).
- RBAC: read-only, nothing more: Pods, namespaces, nodes (platform), ServiceAccounts and Secrets (pull secrets), workload kinds (owner walk). No CRD; the config file is a mounted volume, so it needs no API access.
- Tests: envtest for the controller, a fixture-based test for the xeol JSON parser. e2e (`task test-e2e`) is a smoke test: Helm install into k3d (no traefik) under restricted PSA, wait for Ready. Fixtures in `internal/scanner/testdata/` are real syft (CycloneDX), xeol and trivy output, kept locally only: the directory is gitignored except `.gitkeep`, and fixture tests skip when a file is missing. Regenerate them from the test images (`node:16-alpine`, `python:3.8-slim`, `python:3.12-slim`) with the operator's cataloger list.

## Layout

```
internal/config/     config file struct, strict load, file watcher (live reload)
internal/controller/ Pod reconciler, inventory, live config snapshot
internal/scanner/    syft, xeol and trivy exec + JSON parsing, scan queue
internal/eoldb/      build xeol.db from endoflife.date, DB manager
internal/vulndb/     download trivy's vulnerability DB, DB manager (--trivy)
internal/dbloop/     refresh/readiness loop and atomic swap shared by both DB managers
internal/metrics/    metric definitions and updates
internal/api/        JSON API for Grafana (Infinity), HTML UI (`ui/page.html`, htmx), MCP tools
internal/results/    SQLite store of each digest's last scan
config/              kustomize manifests (kubebuilder, dev only)
helm/xeol-operator/  Helm chart: supported install, config ConfigMap, alert rules, dashboards/xeol.json and xeol-trivy.json
ui/                  docs site (Astro Starlight): guides in src/content/docs/*.mdx, published to GitHub Pages
.github/workflows/   ci.yml, release.yml (image to Docker Hub, chart attached to the release), pages.yml (docs + Helm repo)
Taskfile.yml
```

## Local test cluster

- Use the k3d cluster, context `k3d-k3s-default` (k3s v1.31, containerd, single node).
- The kubeconfig also holds production contexts (EKS, AKS). Always pass `--context k3d-k3s-default` / `--kube-context k3d-k3s-default` explicitly on every `kubectl` and `helm` command. Never rely on the current context, and never run against any other context.
- Load locally built images with `k3d image import <image> -c k3s-default` instead of pushing to a registry.
- kube-prometheus-stack (namespace `monitoring`, release `kube-prom-stack`) picks up ServiceMonitors, PrometheusRules and dashboard ConfigMaps from any namespace.
- Fresh setup: `task cluster-up` (k3d cluster, kube-prometheus-stack, EOL test apps in namespace `test`, operator). Redeploy the operator with `task deploy-dev -- -f test/dev-values.yaml`.
- Images loaded with `k3d image import` have no registry digest (`imageID: sha256:...`), so the operator skips them; that includes the operator itself.

## Docs and releases

- `ui/` uses pnpm (version in `ui/package.json` `packageManager`, lockfile `pnpm-lock.yaml`), locally and in `pages.yml`; not npm.
- The guides live only in `ui/src/content/docs/*.mdx` (Astro Starlight; `docs/` is gone). Link between them as `/xeol-operator/<page>/#<anchor>`; `task docs-build` fails on broken internal links. Keep the README as the GitHub landing page and link it to the site, not to files.
- Publishing a GitHub release `vX.Y.Z`: `release.yml` pushes `docker.io/rishang/xeol-operator:vX.Y.Z` and `:X.Y.Z`, then packages the chart as version/appVersion `X.Y.Z`, attaches the `.tgz` to the release, and calls `pages.yml`.
- `pages.yml` builds the site and the Helm repository together (`https://rishang.github.io/xeol-operator`: `index.yaml` from every release's chart asset, `charts/`, `artifacthub-repo.yml`). Nothing lives in a `gh-pages` branch. The site also serves `/llms.txt`, `/llms-small.txt` and `/llms-full.txt`, generated from the guides by `starlight-llms-txt` (pinned in `ui/package.json`). It also runs on pushes to `main` that touch `ui/`.

## Git

Don't commit, push, or create branches/PRs unless explicitly asked. Leave changes in the working tree.
