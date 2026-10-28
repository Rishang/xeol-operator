# Helm setup

The chart at `helm/xeol-operator` is the supported install. It runs one operator pod (syft and xeol bundled), creates `XeolScanner/default`, and can wire up Prometheus, alerts and a Grafana dashboard.

## Requirements

- Helm 3.
- Egress to `endoflife.date` (or see [air-gapped.md](air-gapped.md)) and to the registries your images come from.
- Optional: the Prometheus Operator (e.g. kube-prometheus-stack) for the ServiceMonitor and PrometheusRule, and Grafana with its sidecar plus the [Infinity](https://grafana.com/grafana/plugins/yesoreyeram-infinity-datasource/) plugin for the dashboard.

## Install

Minimal (metrics only):

```bash
helm install xeol-operator ./helm/xeol-operator -n xeol-system --create-namespace
kubectl get xeolscanner default
```

With kube-prometheus-stack and Grafana:

```bash
# serviceMonitor.enabled     Prometheus scrapes the EOL metrics (PromQL, history). Needs the Prometheus Operator.
# prometheusRule.enabled     Alert rules: EOL software running or due soon, stale DB, failing scans. Needs the ServiceMonitor.
# grafanaDatasource.enabled  Infinity datasource + dashboard, loaded by Grafana's sidecars. Needs the Infinity plugin.
helm install xeol-operator ./helm/xeol-operator -n xeol-system --create-namespace \
  --set serviceMonitor.enabled=true \
  --set prometheusRule.enabled=true \
  --set grafanaDatasource.enabled=true
```

The pod turns Ready once the first EOL database build succeeds. Scans start as soon as `XeolScanner/default` exists.

Check it:

```bash
kubectl get xeolscanner default
kubectl -n xeol-system logs deploy/xeol-operator | grep -E 'scanned|scan failed'
kubectl -n xeol-system port-forward svc/xeol-operator-metrics 8080
curl -s localhost:8080/metrics | grep ^xeol_
```

## What gets installed

| Kind | Name | When |
|---|---|---|
| CustomResourceDefinition | `xeolscanners.xeol.io` | always (`crds/`, install only) |
| ServiceAccount, ClusterRole, ClusterRoleBinding | `xeol-operator` | always |
| Role, RoleBinding | `xeol-operator-leader-election` | always |
| Service | `xeol-operator-metrics` (port 8080: `/metrics`, `/api/v1/*`) | always |
| Deployment | `xeol-operator` | always |
| XeolScanner | `default` | always, from `scanner.*` |
| ServiceMonitor | `xeol-operator` | `serviceMonitor.enabled` |
| PrometheusRule | `xeol-operator` | `prometheusRule.enabled` |
| ConfigMap | `xeol-operator-datasource` | `grafanaDatasource.enabled` |
| ConfigMap | `xeol-operator-dashboard` | `grafanaDatasource.enabled` |

RBAC is read-only: Pods, Namespaces, ServiceAccounts, Nodes, Secrets (only to read image pull secrets), apps/batch workloads, plus the XeolScanner CRD and its status.

## Features

The operator always scans and always serves `/metrics` and the JSON API (`/api/v1/*`) on port 8080. Everything else is opt-in. Each feature reads the same in-memory results; none depends on another unless noted.

| Feature | Value | Gives you | Needs |
|---|---|---|---|
| Metrics collection | `serviceMonitor.enabled` | EOL findings in Prometheus: PromQL, history | Prometheus Operator |
| Alerts | `prometheusRule.enabled` | Notifications through Alertmanager | Prometheus Operator, and the ServiceMonitor for data |
| Grafana dashboard | `grafanaDatasource.enabled` | Datasource + ready-made dashboard | Grafana sidecars, Infinity plugin |
| Built-in web page | `ui.enabled` | Dashboard without Grafana | A browser and `kubectl port-forward` |
| MCP tools | `mcp.enabled` | Ask an AI assistant about EOL findings | An MCP client and `kubectl port-forward` |
| EOL data source | `db.source` | Online (daily from endoflife.date) or a mounted DB | Egress, or a DB you ship |

### Metrics collection: `serviceMonitor.enabled`

**Use it to** get findings into Prometheus, so you can query them with PromQL, keep history, and build your own panels or alerts.

**Creates** a `ServiceMonitor` that tells the Prometheus Operator to scrape `xeol-operator-metrics:8080/metrics` every `serviceMonitor.interval` (60s).

**How it works:** on each scrape the operator answers from memory, so an image that stops running loses its series straight away. Metrics are listed in the [README](../README.md#metrics).

**Leave it off** if you have no Prometheus Operator. You can still scrape the Service yourself, or skip Prometheus entirely and use the dashboard, web page or MCP, which don't need it.

**Check:** the target `serviceMonitor/xeol-system/xeol-operator` is `UP` in Prometheus → Status → Targets. kube-prometheus-stack by default only selects ServiceMonitors labelled `release: <its release>`; if yours does, set `serviceMonitor.labels.release`.

### Alerts: `prometheusRule.enabled`

**Use it to** be told about EOL software instead of having to look. It's only alert rules (no recording rules), so nothing else depends on it.

**Creates** a `PrometheusRule`. The Prometheus Operator loads it into Prometheus, which evaluates it every minute and sends firing alerts to Alertmanager, which routes them to Slack, PagerDuty, email and so on.

| Alert | Fires when | Severity |
|---|---|---|
| `XeolEOLSoftwareRunning` | Software already past EOL runs, for 10 minutes. | warning |
| `XeolEOLSoftwareSoon` | Software reaches EOL within `prometheusRule.eolSoonDays` (90), for 1 hour. | info |
| `XeolDatabaseStale` | The EOL database is older than `prometheusRule.dbStaleDays` (30). | warning |
| `XeolDatabaseMissing` | No EOL database for 30 minutes, so nothing is scanned. | warning |
| `XeolScanErrors` | More than 5 scans failed with one reason in the last hour, for 15 minutes. | info |

The last three tell you the operator itself is unhealthy, which an empty dashboard can't: no findings could mean "all good" or "scanning stopped".

Alerts fire per image and product, not per workload. To route by team, set `scanner.workloadLabels: [team]` and write your own rule joining `xeol_image_workload` on `digest`.

**Leave it off** without the Prometheus Operator (the `PrometheusRule` kind won't exist and the install fails), or if you write your own rules. Without the ServiceMonitor the rules have no data and only `XeolDatabaseMissing` would fire.

**Check:** Prometheus → Alerts lists the `xeol-operator` group. Some setups select rules by label; set `prometheusRule.labels.release` then.

### Grafana dashboard: `grafanaDatasource.enabled`

**Use it to** get a ready-made Grafana dashboard: headline stats, EOL by namespace and workload, a per-workload product table (red past EOL, yellow within 6 months) and the full findings list, filterable by namespace, kind and an upcoming window.

![Grafana dashboard](../.github/images/grafana-dashboard.png)

**Creates** two ConfigMaps in the operator's namespace:

- `xeol-operator-datasource`, labelled `grafana_datasource: "1"`: an [Infinity](https://grafana.com/grafana/plugins/yesoreyeram-infinity-datasource/) datasource (uid `xeol-operator`) pointing at `http://xeol-operator-metrics.<namespace>.svc:8080`.
- `xeol-operator-dashboard`, labelled `grafana_dashboard: "1"`: the dashboard JSON.

**How it works:** the chart never contacts Grafana. Grafana's sidecar containers (`grafana-sc-datasources`, `grafana-sc-dashboard` in the Grafana pod) watch for ConfigMaps with those labels, write them into Grafana's provisioning folders and reload. The dashboard's panels then query the operator's JSON API from the Grafana server, inside the cluster. It doesn't read Prometheus, so it works without the ServiceMonitor.

**Needs** on the Grafana side:

- the Infinity plugin (kube-prometheus-stack: `grafana.plugins: [yesoreyeram-infinity-datasource]`);
- the sidecars watching the operator's namespace: `grafana.sidecar.datasources.searchNamespace` and `grafana.sidecar.dashboards.searchNamespace` set to `ALL` (or including it);
- matching labels: change `grafanaDatasource.labels` / `grafanaDashboard.labels` if your sidecars use others; `grafanaDashboard.annotations` can set a folder (e.g. `grafana_folder: Security`);
- network access from Grafana to the operator's Service on port 8080.

**Leave it off** without Grafana sidecars. Then add an Infinity datasource with uid `xeol-operator` and the URL above by hand, and import `helm/xeol-operator/dashboards/xeol.json`.

**Check:** `kubectl -n <grafana-ns> logs deploy/<grafana> -c grafana-sc-datasources | grep xeol` shows `Writing .../xeol-operator-datasource.yaml`.

### Built-in web page: `ui.enabled`

**Use it** in clusters without Grafana, or for a quick look without setting up dashboards.

![Built-in web page](../.github/images/web-ui.png)

**Serves** a page at `/` on port 8080:

- tiles: workloads past EOL and due soon, days to the next EOL, EOL database age, failed scans by reason (hover ⓘ for the affected workloads);
- a timeline with one dot per product release by EOL date; click a dot to filter the findings;
- a findings table with sorting, per-column filters and resizable columns;
- workload and namespace summaries, and namespace, kind, text and window filters.

```bash
kubectl -n xeol-system port-forward deploy/xeol-operator 8080   # then open http://localhost:8080/
```

**How it works:** the operator renders the HTML. The browser loads htmx 2.0.4 from jsDelivr (pinned, integrity-checked) to update in place; without internet access in the browser, filters still work by reloading the page. Nothing is stored; it's read-only.

**Leave it off** if nobody will use it. It has no authentication: reach it by port-forward, never through a public Ingress.

### MCP tools: `mcp.enabled`

**Use it to** ask an AI assistant (Claude Code, Claude Desktop, or any MCP client) questions like "which prod workloads run EOL software?".

**Serves** an MCP endpoint at `/mcp` (Streamable HTTP, stateless) with four read-only tools: `status`, `namespaces`, `workloads`, `findings`. Each takes optional `namespace`, `kind` and `within_days` and returns the same JSON as the matching `/api/v1/*` endpoint.

```bash
kubectl -n xeol-system port-forward deploy/xeol-operator 8080
claude mcp add --transport http xeol http://localhost:8080/mcp
```

**Leave it off** if you don't use MCP clients. Like the web page it has no authentication; cross-origin browser requests are refused, so a web page can't call it.

### EOL data: `db.source`

**`online` (default):** the operator builds xeol's database from [endoflife.date](https://endoflife.date) at start and every day. The pod is Ready after the first build. A failed rebuild keeps the previous database and retries the next day. Needs egress to `db.url` (or an internal mirror of it).

**`mounted`:** for air-gapped clusters. Build the database elsewhere and mount it read-only, from a volume or copied out of an image by an init container. See [air-gapped.md](air-gapped.md).

Either way, `xeol_db_built_timestamp_seconds`, the `DBStale` condition and the `XeolDatabaseStale` alert tell you when the data is old.

### Always on: JSON API and private registries

- **JSON API:** `GET /api/v1/{status,namespaces,workloads,findings}` on port 8080, with `namespace`, `kind` (comma-separated) and `within_days` (default 180). For scripts and the Grafana dashboard. Read-only, no authentication.
- **Private registries:** no setup. Each scan uses the `imagePullSecrets` of a Pod running the image and of its ServiceAccount, written to a temporary file and deleted after the scan.
- **High availability:** `replicas > 1` runs standby pods via leader election. Only the leader scans and serves data; the others answer 503 (the web page shows "on standby").

## Values

### Scan scope → `XeolScanner/default`

| Value | Default | Field |
|---|---|---|
| `scanner.namespaceSelector` | `{}` (all) | `spec.namespaceSelector` |
| `scanner.excludeNamespaces` | `["kube-*"]` | `spec.excludeNamespaces` |
| `scanner.excludeImages` | `[]` | `spec.excludeImages` |
| `scanner.interval` | `24h` | `spec.interval` |
| `scanner.workloadKinds` | `[]` (CRD default: Deployment, StatefulSet, DaemonSet, Job, CronJob) | `spec.workloadKinds` |
| `scanner.workloadLabels` | `[]` | `spec.workloadLabels` |
| `scanner.ignoreProducts` | `[]` | `spec.ignoreProducts` (accepted findings to hide) |

Field behaviour, globs and status: [crd.md](crd.md). If you'd rather manage the CR yourself (e.g. with GitOps), edit it directly; a later `helm upgrade` will overwrite it from values.

### Scan resources → operator flags

| Value | Default | Notes |
|---|---|---|
| `scanner.workers` | `1` | Concurrent scans. Each needs its own memory headroom. |
| `scanner.timeout` | `10m` | Per image. |
| `scanner.goMemLimit` | `800MiB` | `GOMEMLIMIT` for syft and xeol. Keep it below `resources.limits.memory`. |
| `scanner.maxProcs` | `2` | `GOMAXPROCS` for syft and xeol. |
| `resources` | requests 100m / 256Mi, limit 1Gi | An image whose scan needs more than the limit OOM-kills the container once, is then skipped for 24h and reported as `xeol_scan_errors_total{reason="oom"}`. |
| `scanTmp.sizeLimit` | `4Gi` | `/tmp` emptyDir for image layers during a scan. |

Small images scan in about 200 MB; large ones (e.g. grafana) peak around 0.7 GB. Raise `resources.limits.memory` and `scanner.goMemLimit` together if you see `reason="oom"`.

### EOL database

| Value | Default | Notes |
|---|---|---|
| `db.source` | `online` | `online`: built from endoflife.date at start and daily. `mounted`: read a prebuilt DB. |
| `db.url` | endoflife.date `products/full` | Point at an internal mirror if needed. |
| `db.mounted.path` | `/xeol-db` | |
| `db.mounted.volume` | `{}` | Any volume source holding `<path>/1/xeol.db`, mounted read-only. |
| `db.mounted.initContainer.image` | `""` | Copy the DB out of an image at startup; wins over `volume`. |

Recipes: [air-gapped.md](air-gapped.md).

### Prometheus and Grafana

| Value | Default | Notes |
|---|---|---|
| `metrics.port` | `8080` | Plain HTTP; `/metrics`, the JSON API, and the UI and MCP when enabled. |
| `ui.enabled` | `false` | Server-rendered HTML UI at `/` (findings, workloads, namespaces). The browser loads htmx 2.0.4 from jsDelivr (pinned, SRI); without it filters still work by reload. |
| `mcp.enabled` | `false` | MCP over HTTP (Streamable HTTP, stateless) at `/mcp`: read-only tools `status`, `namespaces`, `workloads`, `findings`. |
| `serviceMonitor.enabled` / `interval` / `labels` | `false` / `60s` / `{}` | Add e.g. `{release: kube-prom-stack}` if your Prometheus selects by label. |
| `prometheusRule.enabled` / `labels` | `false` / `{}` | Alert rules only; see [Alerts](#alerts-prometheusruleenabled). |
| `prometheusRule.eolSoonDays` | `90` | Threshold for `XeolEOLSoftwareSoon`. |
| `prometheusRule.dbStaleDays` | `30` | Threshold for `XeolDatabaseStale`. |
| `grafanaDatasource.enabled` | `false` | Infinity datasource (uid `xeol-operator`) pointing at the JSON API, plus the dashboard that uses it. |
| `grafanaDashboard.labels` / `annotations` | `grafana_dashboard: "1"` / `{}` | The dashboard ConfigMap, installed with the datasource and picked up by Grafana's dashboard sidecar; use `annotations` for a folder. |

What each of these does and needs: [Features](#features).

### Pod

`image.repository`, `image.tag` (defaults to the chart `appVersion`), `image.pullPolicy`, `imagePullSecrets` (for the operator image itself), `replicas` (extra replicas are standby via leader election), `podAnnotations`, `nodeSelector`, `tolerations`, `affinity`, `priorityClassName`.

## Example values file

```yaml
scanner:
  namespaceSelector:
    matchLabels: {xeol-scan: "true"}
  excludeNamespaces: ["kube-*", "monitoring"]
  workloadLabels: [team]
  interval: 24h
  ignoreProducts:
    - products: [perl]
      namespaces: [test]
  goMemLimit: 1600MiB                 # below the 2Gi limit
resources:
  limits: {memory: 2Gi}
  requests: {cpu: 100m, memory: 256Mi}
serviceMonitor:
  enabled: true
  labels: {release: kube-prom-stack}
prometheusRule:
  enabled: true
  labels: {release: kube-prom-stack}
grafanaDatasource:
  enabled: true                     # datasource + dashboard
```

```bash
helm upgrade --install xeol-operator ./helm/xeol-operator -n xeol-system --create-namespace -f my-values.yaml
```

## Upgrade

```bash
kubectl apply --server-side -f helm/xeol-operator/crds/   # Helm never upgrades CRDs
helm upgrade xeol-operator ./helm/xeol-operator -n xeol-system --reuse-values
```

A new pod starts with an empty state volume and rescans everything.

## Uninstall

```bash
helm uninstall xeol-operator -n xeol-system
kubectl delete crd xeolscanners.xeol.io   # Helm leaves CRDs behind
```
