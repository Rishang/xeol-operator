# AGENTS.md

## Goal

Kubernetes operator that runs [xeol](https://github.com/xeol-io/xeol) (end-of-life package scanner) against container images in a cluster and exposes the results as Prometheus metrics, so EOL findings can be graphed and alerted on in Grafana.

```
cluster workloads ──> operator collects images ──> xeol scan ──> /metrics ──> Prometheus ──> Grafana
```

## Scope

In:
- Discover images from running Pods (all namespaces, or those selected by the CR).
- Scan each unique image digest with xeol, cache results by digest, rescan on an interval.
- Export findings on a Prometheus `/metrics` endpoint, plus alert rules.
- Ship a Helm chart (`helm/xeol-operator`) with an optional ServiceMonitor, PrometheusRule and Grafana dashboard.
- For clusters without Grafana: an optional built-in HTML UI (`ui.enabled`) and MCP over HTTP (`mcp.enabled`).

Out (until asked for):
- Vulnerability scanning (that's grype/trivy).
- An external database, or pushing to Grafana. Grafana reads Prometheus and the operator's JSON API.
- Auth, Ingress or write actions for the built-in UI/MCP.
- Webhooks or admission control that block EOL images.

## Stack

- Go, [kubebuilder](https://book.kubebuilder.io/) / controller-runtime layout.
- syft and xeol run as binaries (both bundled in the operator image): syft writes the SBOM, xeol matches it with `-o json`. Shell out, parse the JSON. Don't vendor xeol or syft as libraries unless the CLIs can't do the job.
- Metrics through controller-runtime's built-in Prometheus registry (`sigs.k8s.io/controller-runtime/pkg/metrics`). No separate exporter process.
- Taskfile (`Taskfile.yml`) is the task runner. It replaces kubebuilder's Makefile; add targets there rather than documenting raw commands.

## Decisions (2026-10-26)

Recorded in `.local/plan/xeol-operator-mvp.plan.html`.
- **Scanner:** xeol behind a boundary. `internal/scanner` returns flat `Finding` rows; nothing xeol-specific leaks past it.
- **EOL data:** the operator builds xeol's DB itself from [endoflife.date](https://endoflife.date/api/v1/products/full) (xeol's own DB stopped updating 2026-09-02 and never matched distros). xeol is only the matcher. Never contact `data.xeol.io`.
- **Export:** Prometheus `/metrics` for alerts, plus a read-only JSON API (`/api/v1/{status,namespaces,workloads,findings}`, `internal/api`) on the metrics port for the Grafana dashboard via the Infinity datasource (D5, 2026-10-27). Both read the in-memory inventory. No Loki, no external database.
- **UI and MCP (2026-10-27):** `--ui` serves a server-rendered page (`internal/api/ui/page.html`, Go `html/template`) at `/`; htmx 2.0.4 from jsDelivr (pinned, SRI) swaps in `/ui/report` on filter changes (and the Refresh button), and without it the filters form does a plain reload; `--mcp` serves the same four endpoints as read-only MCP tools at `/mcp` (official Go SDK, stateless Streamable HTTP, cross-origin protected). Same port, no auth, off by default; non-leaders answer 503.
- **Lookahead:** always scan with `-l 10y`; export the EOL date as the metric value. "How close counts" is a PromQL threshold, not operator config.
- **Stale DB:** disable xeol's age check (`XEOL_DB_VALIDATE_AGE=false`), export `xeol_db_built_timestamp_seconds`, alert on age.

## API (initial)

One singleton CRD `XeolScanner` (`name: default`):

```yaml
apiVersion: xeol.io/v1alpha1
kind: XeolScanner
metadata:
  name: default
spec:
  namespaceSelector: {}        # empty = all namespaces
  excludeNamespaces: [kube-*]  # globs
  excludeImages: []            # globs on image ref
  interval: 24h                # rescan each image digest this often
  workloadKinds: [Deployment, StatefulSet, DaemonSet, Job, CronJob]  # default; [] = every kind
  workloadLabels: [team]       # copied onto metrics as k8s_label_<name>
  ignoreProducts:              # hide accepted findings at export time (no rescan)
    - products: [perl]         # endoflife.date slugs; optional cycles, namespaces, selector (workload labels)
      namespaces: [test]
status:
  lastSweepTime: ...
  imagesScanned: 42
  eolFindings: 7
  dbBuilt: ...
  conditions: []               # Ready, DBStale
```

Globs (`excludeNamespaces`, `excludeImages`, `ignoreProducts[].namespaces`): only `*` (anything, `/` included) and `?` (one character) are special; every string is a valid glob.

Pod-level settings are operator flags set by Helm, not CR fields: `--scan-workers` (default 1), `--scan-memory-limit` (GOMEMLIMIT for xeol), `--scan-max-procs` (GOMAXPROCS), `--db-source` (`online` | `mounted`), `--db-dir`, `--state-dir`.

Add fields only when a real use case needs them.

## Metrics

Prefix `xeol_`. Keep label cardinality bounded: one series per (digest, product, cycle), not per Pod. No path, purl or timestamp labels.

| Metric | Type | Labels / value |
|---|---|---|
| `xeol_eol_date_seconds` | gauge, value = EOL date (unix) | `image`, `digest`, `product`, `cycle`, `package`, `version`, `url` (endoflife.date page) |
| `xeol_image_workload` | gauge (1) | `digest`, `namespace`, `kind`, `name`, `k8s_label_*` (join in Grafana) |
| `xeol_db_built_timestamp_seconds` | gauge | — |
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
                                │  exec: syft scan registry:<repo>@<digest> -o syft-json=sbom.json
                                │        xeol sbom:sbom.json -o json -q -l 10y
                                ▼
                         Result cache  digest -> {findings, scannedAt, err}
                                │
                                ▼
           Prometheus  <──  /metrics (Collector reads Inventory + cache)  ──> alerts
           Grafana     <──  /api/v1/* JSON (Infinity datasource, same port)

 DB builder (ticker, daily): endoflife.date → xeol.db → shared cache dir on emptyDir
```

- **Image identity:** scan by the digest in `pod.status.containerStatuses[].imageID`, not the tag, so results match what actually runs. Dedupe by digest across the cluster.
- **Scan execution:** in-process `exec`, one worker by default, two steps per image: the bundled syft catalogs the image into an SBOM (xeol's cataloger selection, `catalogers` in `internal/scanner/xeol.go`), then xeol matches `sbom:<file>`. xeol v0.10.8's built-in syft v1.10 needs over twice the memory (grafana: 1.6 GB vs 0.78 GB, same findings). The operator only bounds the cost (memory limit, `GOMEMLIMIT`, `GOMAXPROCS=2`, timeout), never reimplements cataloging. An image that OOM-kills the pod leaves an in-flight marker on the state emptyDir; it is skipped for 24h as `reason="oom"` instead of crash-looping.
- **Registry auth:** per scan, a temp `config.json` from the Pod's (and its ServiceAccount's) `imagePullSecrets` in an `emptyDir` as `DOCKER_CONFIG`, deleted after.
- **EOL DB (`--db-source=online`):** `internal/eoldb` builds xeol's DB from endoflife.date at startup and daily; readiness waits for the first good DB. Scans run with `XEOL_DB_AUTO_UPDATE=false`. A build swaps in only when complete: a failed rebuild keeps the old DB, logs it, and retries next day (every 30s if there is no usable DB). `--db-source=mounted` reads a read-only volume instead (air-gapped, `docs/air-gapped.md`).
- **Rescans:** each digest `spec.interval` after its own last scan; a DB rebuild triggers none (decided 2026-10-27, avoids bunching scans). Results hold the EOL date and "already EOL" is computed at query time, so only endoflife.date data changes wait for a rescan (≤ ~2× interval).
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
- Reconcile is idempotent; scans run off the reconcile loop through a bounded worker queue keyed by digest.
- Private registries: reuse the Pod's `imagePullSecrets`. Never log credentials.
- Run as non-root, read-only root filesystem; xeol's DB cache goes on an `emptyDir` (or PVC if configured).
- RBAC: read-only, nothing more: Pods, namespaces, nodes (platform), ServiceAccounts and Secrets (pull secrets), workload kinds (owner walk), plus the CRD.
- Tests: envtest for the controller, a fixture-based test for the xeol JSON parser. e2e (`task test-e2e`) is a smoke test: Helm install into k3d (no traefik) under restricted PSA, wait for Ready. Keep the parser test fed with real xeol output saved under `testdata/`.

## Layout

```
api/v1alpha1/        CRD types
internal/controller/ reconciler
internal/scanner/    xeol exec + JSON parsing, scan queue
internal/eoldb/      build xeol.db from endoflife.date, DB manager
internal/metrics/    metric definitions and updates
internal/api/        JSON API for Grafana (Infinity), HTML UI (`ui/page.html`, htmx), MCP tools
internal/results/    SQLite store of each digest's last scan
config/              kustomize manifests (kubebuilder, dev only)
helm/xeol-operator/  Helm chart: supported install, CRD copy, alert rules, dashboards/xeol.json
Taskfile.yml
```

## Local test cluster

- Use the k3d cluster, context `k3d-k3s-default` (k3s v1.31, containerd, single node).
- The kubeconfig also holds production contexts (EKS, AKS). Always pass `--context k3d-k3s-default` / `--kube-context k3d-k3s-default` explicitly on every `kubectl` and `helm` command. Never rely on the current context, and never run against any other context.
- Load locally built images with `k3d image import <image> -c k3s-default` instead of pushing to a registry.
- kube-prometheus-stack (namespace `monitoring`, release `kube-prom-stack`) picks up ServiceMonitors, PrometheusRules and dashboard ConfigMaps from any namespace.
- Deploy the operator with `task deploy-dev -- --set serviceMonitor.enabled=true,prometheusRule.enabled=true,grafanaDatasource.enabled=true,ui.enabled=true,mcp.enabled=true`. Test workloads with known-EOL images live in namespace `test` (`kubectl --context k3d-k3s-default apply -f test/apps.yaml`).
- Images loaded with `k3d image import` have no registry digest (`imageID: sha256:...`), so the operator skips them; that includes the operator itself.

## Git

Don't commit, push, or create branches/PRs unless explicitly asked. Leave changes in the working tree.
