# xeol-operator

**Is end-of-life software still running in your cluster?** Python 3.8, Node 16 and Alpine 3.18 are past end of life (EOL), but are any of your images still built on them?

xeol-operator scans every image running in your Kubernetes cluster, finds runtimes, databases, frameworks and distros that are past end of life or close to it, and shows you which workloads to fix first. Results go to Grafana, Prometheus alerts, or a built-in web page.

[![CI](https://github.com/Rishang/xeol-operator/actions/workflows/ci.yml/badge.svg)](https://github.com/Rishang/xeol-operator/actions/workflows/ci.yml)
[![Docker Hub](https://img.shields.io/docker/v/rishang/xeol-operator?label=docker%20hub&sort=semver)](https://hub.docker.com/r/rishang/xeol-operator)

![Grafana dashboard: workloads running EOL software, by namespace and workload, with each finding's EOL date and days left](.github/images/grafana-dashboard.png)

## Why use it

- **Covers every running workload.** It reads what Pods actually run (by image digest), not what a manifest claims. Private registries work with the Pods' own pull secrets.
- **Gives dates, not just labels.** Every finding has its EOL date and days left, so "due within 90 days" is just a query.
- **Fits the tools you already have.** It ships Prometheus metrics, ready-made alert rules and a Grafana dashboard with the Helm chart. Without Grafana, turn on the built-in web page or MCP tools for AI assistants.
- **Uses EOL data that's kept up to date.** It builds its database daily from [endoflife.date](https://endoflife.date), covering language runtimes, databases, frameworks and distros (Debian, Alpine, Ubuntu, ...).
- **Is light to run.** It's one read-only pod with no database or extra services, and it works in air-gapped clusters too.

## Quick start

```bash
git clone https://github.com/Rishang/xeol-operator.git && cd xeol-operator

# With kube-prometheus-stack and Grafana (Infinity plugin):
helm install xeol-operator ./helm/xeol-operator -n xeol-system --create-namespace \
  --set serviceMonitor.enabled=true \
  --set prometheusRule.enabled=true \
  --set grafanaDatasource.enabled=true

kubectl get xeolscanner default
# NAME      IMAGES   EOL   DB BUILT   READY
# default   42       7     3m         True
```

**No Grafana?** Use the built-in page instead:

```bash
helm install xeol-operator ./helm/xeol-operator -n xeol-system --create-namespace \
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

The web page, MCP tools and JSON API are read-only and have no login: reach them with `port-forward`, and don't expose them through a public Ingress.

## How it works

```
Pods ──> images by digest ──> syft + xeol scan ──> EOL findings ──┬─> /metrics ──> Prometheus ──> alerts
                                    ▲                              ├─> JSON API ──> Grafana dashboard
             endoflife.date ──> EOL database (rebuilt daily)       └─> web page, MCP tools
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

Add `scanner.workloadLabels: [team]` to label metrics with each workload's team and route every team its own alerts.

## Configure

Scan scope lives in Helm values under `scanner.*`, which the chart writes to the cluster-wide `XeolScanner` named `default`:

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

Changes apply without a restart. Memory, timeouts, workers and the air-gapped DB are covered in [docs/helm.md](docs/helm.md).

## Metrics

| Metric | Value | Labels |
|---|---|---|
| `xeol_eol_date_seconds` | EOL date (Unix seconds) | `image`, `digest`, `product`, `cycle`, `package`, `version`, `url` |
| `xeol_image_workload` | 1 | `digest`, `namespace`, `kind`, `name`, `k8s_label_*` |
| `xeol_db_built_timestamp_seconds` | DB build time | |
| `xeol_scan_duration_seconds` | histogram | |
| `xeol_scan_errors_total` | counter | `reason` (`auth`, `not_found`, `timeout`, `oom`, `other`) |

The EOL date is the metric value, so how close counts is up to your query:

```promql
xeol_eol_date_seconds < time()                       # already EOL
(xeol_eol_date_seconds - time()) / 86400 < 90        # EOL within 90 days
```

Join `xeol_image_workload` on `digest` to get workloads (the dashboard's main table does this).

## Good to know

- Only products tracked by endoflife.date are reported, so "no findings" doesn't prove nothing is EOL.
- Images loaded straight onto nodes, with no registry digest, are skipped.
- Results are saved to a small SQLite file on the pod's volume. A container restart (e.g. after an out-of-memory kill) picks up where it left off; a new pod starts fresh.

## Docs

| | |
|---|---|
| [Usage](docs/usage.md) | Install, read results, tune, troubleshoot |
| [Helm](docs/helm.md) | Every chart value, upgrade, uninstall |
| [CRD](docs/crd.md) | `XeolScanner` fields, globs, status, conditions |
| [Air-gapped](docs/air-gapped.md) | Clusters without internet access |

## Development

```bash
task --list          # all tasks
task test            # unit + envtest
task test-e2e        # Helm smoke test in a throwaway k3d cluster
task cluster-up      # local k3d cluster: kube-prometheus-stack, EOL test apps, operator
task deploy-dev      # rebuild and redeploy the operator to k3d
```

Design and conventions: [AGENTS.md](AGENTS.md). Specs: [openspec/specs/](openspec/specs/).

## License

[Apache 2.0](LICENSE).
