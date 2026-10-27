# xeol-operator

A Kubernetes operator that finds end-of-life (EOL) software in the images your cluster is running, and exports it to Prometheus so Grafana can show and alert on it.

- **What runs:** it watches Pods, dedupes their images by digest, and scans each one with [xeol](https://github.com/xeol-io/xeol) straight from the registry, using the Pods' pull secrets.
- **EOL data:** built daily from [endoflife.date](https://endoflife.date) into xeol's database format, covering language runtimes, databases, frameworks and distros (Debian, Alpine, Ubuntu, ...). xeol's own database stopped updating in September 2026, so it isn't used.
- **Output:** Prometheus metrics, alert rules and a Grafana dashboard; optionally a built-in HTML UI and MCP tools for clusters without Grafana. No database, no extra services.

## Install

```bash
# serviceMonitor.enabled     Prometheus scrapes the EOL metrics (PromQL, history). Needs the Prometheus Operator.
# prometheusRule.enabled     Alert rules: EOL software running or due soon, stale DB, failing scans. Needs the ServiceMonitor.
# grafanaDatasource.enabled  Infinity datasource + dashboard, loaded by Grafana's sidecars. Needs the Infinity plugin.
helm install xeol-operator ./helm/xeol-operator -n xeol-system --create-namespace \
  --set serviceMonitor.enabled=true \
  --set prometheusRule.enabled=true \
  --set grafanaDatasource.enabled=true
kubectl get xeolscanner default
```

The ServiceMonitor and PrometheusRule need the Prometheus Operator (e.g. kube-prometheus-stack); the dashboard needs Grafana's sidecar and the Infinity plugin. Without them, leave those off and scrape the `xeol-operator-metrics` Service on port 8080.

No Grafana? Add `--set ui.enabled=true --set mcp.enabled=true`, then:

```bash
kubectl -n xeol-system port-forward deploy/xeol-operator 8080
# browser: http://localhost:8080/
claude mcp add --transport http xeol http://localhost:8080/mcp   # tools: status, namespaces, workloads, findings
```

Both are read-only and unauthenticated, like the JSON API on the same port: reach them by port-forward, don't put them behind a public Ingress.

- Getting started, reading results, tuning and troubleshooting: [docs/usage.md](docs/usage.md)
- Helm setup, every value, upgrade and uninstall: [docs/helm.md](docs/helm.md)
- The `XeolScanner` CRD: fields, globs, status and conditions: [docs/crd.md](docs/crd.md)
- Air-gapped clusters: [docs/air-gapped.md](docs/air-gapped.md)

## Configure

Scan scope lives on the cluster-scoped `XeolScanner` named `default`; the chart renders it from `scanner.*` values:

```yaml
apiVersion: xeol.io/v1alpha1
kind: XeolScanner
metadata:
  name: default
spec:
  namespaceSelector: {}          # empty = all namespaces
  excludeNamespaces: ["kube-*"]  # globs
  excludeImages: []              # globs on the image reference
  interval: 24h                  # rescan each image digest this often
  workloadLabels: [team]         # copied onto metrics as k8s_label_team
  ignoreProducts:                # accepted findings, hidden from metrics/API/status
    - products: [perl]           # endoflife.date slug
      namespaces: [test]         # optional; also cycles, selector
```

Pod-level settings are Helm values: `scanner.workers` (concurrent scans, default 1), `resources` (default limit 1Gi; an image whose scan needs more is skipped and reported as `reason="oom"`), `scanner.timeout`, `db.source` (`online` | `mounted`), `scanTmp.sizeLimit`. Details: [docs/crd.md](docs/crd.md), [docs/helm.md](docs/helm.md).

## Metrics

| Metric | Value | Labels |
|---|---|---|
| `xeol_eol_date_seconds` | EOL date (Unix seconds) | `image`, `digest`, `product`, `cycle`, `package`, `version`, `url` (endoflife.date page) |
| `xeol_image_workload` | 1 | `digest`, `namespace`, `kind`, `name`, `k8s_label_*` |
| `xeol_db_built_timestamp_seconds` | DB build time | |
| `xeol_scan_duration_seconds` | histogram | |
| `xeol_scan_errors_total` | counter | `reason` (`auth`, `not_found`, `timeout`, `oom`, `other`) |

The EOL date is the value, so "how close counts" is a query, not a setting:

```promql
xeol_eol_date_seconds < time()                       # already EOL
(xeol_eol_date_seconds - time()) / 86400 < 90        # EOL within 90 days
```

To see workloads, join `xeol_image_workload` on `digest` (the dashboard's main table does this).

## State

Scan results are kept in memory and saved to a small SQLite file on the pod's state volume, so a container restart (e.g. after an out-of-memory kill on a very large image) serves the last results immediately instead of rescanning everything. Replacing the pod (upgrade, rollout) starts fresh.

## Coverage

Only products tracked by endoflife.date are reported; "no findings" doesn't mean nothing is EOL. Images loaded directly onto nodes without a registry digest are skipped.

## Development

```bash
task --list          # all tasks
task test            # unit + envtest
task run             # run locally against the k3d cluster
task deploy-dev      # build, import into k3d, helm install
task build-db        # build the EOL DB into ./db
```

Design, decisions and conventions: [AGENTS.md](AGENTS.md). Specs: `openspec/changes/xeol-operator-mvp/`.

## License

Apache 2.0.
