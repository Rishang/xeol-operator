# xeol-operator

**Is end-of-life software still running in your cluster?** Python 3.8, Node 16 and Alpine 3.18 are past end of life (EOL), but are any of your images still built on them?

xeol-operator scans every image running in your Kubernetes cluster, finds runtimes, databases, frameworks and distros that are past end of life or close to it, and shows you which workloads to fix first. Turn on [trivy](https://trivy.dev) and the same scan also lists known vulnerabilities (CVEs). Results go to Grafana, Prometheus alerts, or a built-in web page.

[![CI](https://github.com/Rishang/xeol-operator/actions/workflows/ci.yml/badge.svg)](https://github.com/Rishang/xeol-operator/actions/workflows/ci.yml)
[![Docker Hub](https://img.shields.io/docker/v/rishang/xeol-operator?label=docker%20hub&sort=semver)](https://hub.docker.com/r/rishang/xeol-operator)

![Grafana dashboard: workloads running EOL software, by namespace and workload, with each finding's EOL date and days left](.github/images/grafana-dashboard.png)

## Why use it

- **Covers every running workload.** It reads what Pods actually run (by image digest), not what a manifest claims. Private registries work with the Pods' own pull secrets.
- **Gives dates, not just labels.** Every finding has its EOL date and days left, so "due within 90 days" is just a query.
- **Fits the tools you already have.** It ships Prometheus metrics, ready-made alert rules and a Grafana dashboard with the Helm chart. Without Grafana, turn on the built-in web page or MCP tools for AI assistants.
- **Uses EOL data that's kept up to date.** It builds its database daily from [endoflife.date](https://endoflife.date), covering language runtimes, databases, frameworks and distros (Debian, Alpine, Ubuntu, ...).
- **Finds CVEs too, at little extra cost.** With `trivy.enabled`, trivy matches the SBOM already built for the EOL scan, so images aren't pulled twice. It adds about 0.2 s and 150 MB per image.
- **Is light to run.** It's one read-only pod with no database or extra services, and it works in air-gapped clusters too.

## Quick start

```bash
helm repo add xeol-operator https://rishang.github.io/xeol-operator
helm repo update

# With kube-prometheus-stack and Grafana (Infinity plugin):
helm install xeol-operator xeol-operator/xeol-operator -n xeol-system --create-namespace \
  --set serviceMonitor.enabled=true \
  --set prometheusRule.enabled=true \
  --set grafanaDatasource.enabled=true

kubectl -n xeol-system port-forward svc/xeol-operator-metrics 8080 &
curl -s localhost:8080/api/v1/status   # images_scanned, workloads_eol, last_sweep, ...
```

**No Grafana?** Use the built-in page instead:

```bash
helm install xeol-operator xeol-operator/xeol-operator -n xeol-system --create-namespace \
  --set ui.enabled=true --set mcp.enabled=true
kubectl -n xeol-system port-forward deploy/xeol-operator 8080
# open http://localhost:8080/
```

![Built-in web page: tiles, an EOL timeline, a sortable findings table, and workload and namespace summaries](.github/images/web-ui.png)

Ask an AI assistant over MCP:

```bash
claude mcp add --transport http xeol http://localhost:8080/mcp
# "Which workloads in prod run software that is already end of life?"
```

## Vulnerability scanning (optional)

Add trivy to the same scan:

```bash
helm upgrade xeol-operator xeol-operator/xeol-operator -n xeol-system --reuse-values --set trivy.enabled=true
```

- syft builds each image's SBOM once; xeol reads it for EOL software and `trivy sbom` reads it for known vulnerabilities. The image isn't pulled again.
- The pod downloads trivy's database (~1.6 GB) at start and daily, and turns Ready once it has it. Scans never call out. Air-gapped clusters mount it instead ([air-gapped installation](https://rishang.github.io/xeol-operator/air-gapped/#vulnerability-database-trivyenabled)).
- You get a second Grafana dashboard (`xeol: vulnerabilities (trivy)`), a **Trivy** tab on the web page, `/api/v1/vulnerabilities`, per-image counts in Prometheus and a stale-database alert.
- Only want vulnerabilities? Add `--set eol.enabled=false`. At least one scan type stays on.

![Trivy tab of the built-in web page: workloads with CRITICAL and HIGH vulnerabilities, counts by severity, vulnerable packages grouped by severity, and the vulnerabilities table](.github/images/web-ui-trivy.png)

The web page, MCP tools and JSON API are read-only and have no login: reach them with `port-forward`, and don't expose them through a public Ingress.

## How it works

```
Pods ──> images by digest ──> syft SBOM ──┬─> xeol ──> EOL findings ─────────────┬─> /metrics ──> Prometheus ──> alerts
                                           │      ▲                                ├─> JSON API ──> Grafana dashboards
                                           │      endoflife.date (rebuilt daily)   └─> web page, MCP tools
                                           └─> trivy ──> vulnerabilities (optional) ──┘
                                                  ▲
                                                  trivy DB (downloaded daily)
```

Each image is scanned once, no matter how many Pods run it, and rescanned every `interval` (default 24h).

## Alerts

With `prometheusRule.enabled`:

| Alert | Fires when |
|---|---|
| `XeolEOLSoftwareRunning` | A running image contains software past EOL |
| `XeolEOLSoftwareSoon` | Software reaches EOL within 90 days (`prometheusRule.eolSoonDays`) |
| `XeolDatabaseStale` | The EOL database is older than 30 days |
| `XeolDatabaseMissing` | There is no EOL database, so nothing is scanned |
| `XeolScanErrors` | Images keep failing to scan |
| `XeolVulnDatabaseStale` | The trivy database is older than 7 days (`trivy.enabled`) |

Add `scanner.workloadLabels: [team]` to label metrics with each workload's team and route every team its own alerts.

## Configure

Scan scope lives in Helm values under `scanner.*`. The chart renders them into a config file (the ConfigMap `xeol-operator-config`, mounted into the pod), which the operator reloads when it changes:

```yaml
scanner:
  namespaceSelector: {}          # empty = all namespaces
  excludeNamespaces: ["kube-*"]  # globs
  excludeImages: []              # globs on the image reference
  interval: 24h                  # rescan each image this often
  workloadLabels: [team]         # copied onto metrics as k8s_label_team
  ignoreProducts:                # accepted findings, hidden everywhere
    - products: [perl]           # endoflife.date name
      namespaces: [test]         # optional; also cycles, selector
```

Changes apply without a restart, from `helm upgrade` or `kubectl edit configmap xeol-operator-config`; a bad edit is rejected and the previous config stays ([Configuration](https://rishang.github.io/xeol-operator/configuration/)). Memory, timeouts, workers and the air-gapped DB are covered in [Helm setup](https://rishang.github.io/xeol-operator/helm/).

## Metrics

| Metric | Value | Labels |
|---|---|---|
| `xeol_eol_date_seconds` | EOL date (Unix seconds) | `image`, `digest`, `product`, `cycle`, `package`, `version`, `url` |
| `xeol_image_workload` | 1 | `digest`, `namespace`, `kind`, `name`, `k8s_label_*` |
| `xeol_db_built_timestamp_seconds` | DB build time | |
| `xeol_scan_duration_seconds` | histogram | |
| `xeol_vulnerabilities` | count (`trivy.enabled`) | `digest`, `severity`, `fixable` |
| `xeol_vuln_db_updated_timestamp_seconds` | trivy DB publish time (`trivy.enabled`) | |
| `xeol_scan_errors_total` | counter | `reason` (`auth`, `not_found`, `timeout`, `oom`, `other`, `trivy`) |

The EOL date is the metric value, so how close counts is up to your query:

```promql
xeol_eol_date_seconds < time()                       # already EOL
(xeol_eol_date_seconds - time()) / 86400 < 90        # EOL within 90 days
```

Join `xeol_image_workload` on `digest` to get workloads (the dashboard's main table does this). Vulnerabilities are counts per image, never one series per CVE; the CVE list is at `/api/v1/vulnerabilities`:

```promql
sum by (namespace, name) (xeol_image_workload * on(digest) group_left sum by (digest) (xeol_vulnerabilities{severity="CRITICAL"}))
```

## Good to know

- Only products tracked by endoflife.date are reported, so "no findings" doesn't prove nothing is EOL.
- trivy only sees packages syft records (OS and language packages). Software copied into an image outside a package manager, such as a Python built from source, gets no vulnerability rows, though the EOL scan still reports its version.
- Images loaded straight onto nodes, with no registry digest, are skipped.
- Results are saved to a small SQLite file on the pod's volume. A container restart (e.g. after an out-of-memory kill) picks up where it left off; a new pod starts fresh.

## Docs

Full documentation: **<https://rishang.github.io/xeol-operator/>**

| | |
|---|---|
| [Quick start](https://rishang.github.io/xeol-operator/quick-start/) | Install from the Helm repository, first results |
| [Usage](https://rishang.github.io/xeol-operator/usage/) | Install, read results, tune, troubleshoot |
| [Helm](https://rishang.github.io/xeol-operator/helm/) | Every chart value, upgrade, uninstall |
| [Configuration](https://rishang.github.io/xeol-operator/configuration/) | Config file fields, globs, ignore rules, live reload, errors |
| [Air-gapped](https://rishang.github.io/xeol-operator/air-gapped/) | Clusters without internet access |
| [Architecture](https://rishang.github.io/xeol-operator/architecture/) | Components, scan flow, where data is stored, failure handling |

For AI tools, the site also serves [`llms.txt`](https://rishang.github.io/xeol-operator/llms.txt), [`llms-small.txt`](https://rishang.github.io/xeol-operator/llms-small.txt) and [`llms-full.txt`](https://rishang.github.io/xeol-operator/llms-full.txt).

The chart is also listed on [Artifact Hub](https://artifacthub.io) once the repository is registered there; its source is in [`helm/xeol-operator`](helm/xeol-operator).

## Development

```bash
task --list          # all tasks
task test            # unit + envtest
task test-e2e        # Helm smoke test in a throwaway k3d cluster
task cluster-up      # local k3d cluster: kube-prometheus-stack, EOL test apps, operator
task deploy-dev      # rebuild and redeploy the operator to k3d
task docs-dev        # docs site (ui/, Astro Starlight) at http://localhost:4321/xeol-operator/
```

The docs live in `ui/src/content/docs/*.mdx`; to install from a clone instead of the Helm repository, use `helm install xeol-operator ./helm/xeol-operator`.

Design and conventions: [AGENTS.md](AGENTS.md). Specs: [openspec/specs/](openspec/specs/).

## License

[Apache 2.0](LICENSE).
