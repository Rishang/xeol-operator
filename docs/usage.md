# Using xeol-operator

xeol-operator finds end-of-life (EOL) software inside the images your cluster runs: an old Python, a Node.js release past support, a Debian version nobody patches any more. It scans every running image, keeps the results current, and shows them in Grafana, in Prometheus alerts, or in its own small web page.

This guide walks through installing it with Helm, reading the results, and tuning what it scans. For what each optional feature does and needs see [helm.md#features](helm.md#features), for every Helm value [helm.md](helm.md); for every `XeolScanner` field see [crd.md](crd.md); for clusters without internet access see [air-gapped.md](air-gapped.md).

## Before you start

- A Kubernetes cluster and Helm 3.
- Egress from the cluster to `endoflife.date` (the EOL data) and to the registries your images come from. No egress? Follow [air-gapped.md](air-gapped.md).
- Optional: kube-prometheus-stack (or any Prometheus Operator setup) for metrics and alerts, and Grafana with the [Infinity](https://grafana.com/grafana/plugins/yesoreyeram-infinity-datasource/) plugin for the dashboard.

The operator image is published on Docker Hub as [`rishang/xeol-operator`](https://hub.docker.com/r/rishang/xeol-operator); the chart pulls it by default, tagged with the chart's `appVersion`.

## 1. Install

Get the chart from the repository:

```bash
git clone https://github.com/Rishang/xeol-operator.git
cd xeol-operator
```

Then pick the install that matches your cluster.

**With Prometheus and Grafana** (kube-prometheus-stack):

```bash
# serviceMonitor.enabled     Prometheus scrapes the EOL metrics (PromQL, history). Needs the Prometheus Operator.
# prometheusRule.enabled     Alert rules: EOL software running or due soon, stale DB, failing scans. Needs the ServiceMonitor.
# grafanaDatasource.enabled  Infinity datasource + dashboard, loaded by Grafana's sidecars. Needs the Infinity plugin.
helm install xeol-operator ./helm/xeol-operator -n xeol-system --create-namespace \
  --set serviceMonitor.enabled=true \
  --set prometheusRule.enabled=true \
  --set grafanaDatasource.enabled=true
```

If your Prometheus only picks up ServiceMonitors and rules with a given label, add it, e.g. `--set serviceMonitor.labels.release=kube-prom-stack --set prometheusRule.labels.release=kube-prom-stack`.

**Without Grafana**, use the built-in web page and MCP tools instead:

```bash
# ui.enabled   Built-in web page at http://<port-forward>/: findings, timeline, workloads, namespaces.
# mcp.enabled  Read-only MCP tools at /mcp for AI assistants (status, namespaces, workloads, findings).
helm install xeol-operator ./helm/xeol-operator -n xeol-system --create-namespace \
  --set ui.enabled=true \
  --set mcp.enabled=true
```

You can enable both sets of options at once.

To pin a specific operator version, add `--set image.tag=<version>`. To pull through a registry mirror, set `image.repository` (and `imagePullSecrets` if the mirror needs credentials).

## 2. Check it's working

```bash
kubectl get xeolscanner default
```

```
NAME      IMAGES   EOL   DB BUILT   READY
default   42       7     3m         True
```

- **READY** turns `True` once the EOL database is built (about a minute after start) and scanning has begun.
- **IMAGES** grows as images are scanned. Scans run one at a time by default, roughly 5 to 60 seconds per image, so a large cluster takes a while on first start.
- **EOL** is the number of findings already past end of life.

Follow progress in the logs:

```bash
kubectl -n xeol-system logs deploy/xeol-operator -f | grep -E 'scanned|scan failed'
```

## 3. Read the results

Every view shows the same data: each running workload, the EOL products found in its images, the EOL date and how many days are left (negative once past). "Due soon" means within the chosen window, 180 days by default.

### Grafana

With `grafanaDatasource.enabled`, open the **xeol: end-of-life software** dashboard. It has headline numbers, an "EOL products per workload" table (red: past EOL, yellow: due within 6 months), and the full findings list. Use the variables at the top to narrow by namespace and kind, or change the "Upcoming window".

### Built-in web page

With `ui.enabled`:

```bash
kubectl -n xeol-system port-forward deploy/xeol-operator 8080
```

Open <http://localhost:8080/>. The page shows:

- **Tiles**: workloads past EOL, workloads due soon, days to the next EOL date, age of the EOL database, and failed scans by reason. Hover the ⓘ on a failure reason to see which workloads are affected.
- **Timeline**: one dot per product release, placed by EOL date. Dots left of the "today" line are already past EOL. Click a dot to filter the findings table to that release; click it again to clear.
- **Findings table**: click a column name to sort; use its funnel icon to filter by text or tick specific values. Drag a header's corner to resize a column.
- **Workloads** and **Namespaces**: counts per workload and per namespace, worst first.

Use the Namespace, Kind, Filter rows and Upcoming window controls at the top to narrow everything; **Refresh** reloads the data.

### Prometheus and PromQL

The operator exposes metrics on port 8080 at `/metrics`. The EOL date is the metric's value, so you choose "how soon counts" in the query:

```promql
# already past EOL
xeol_eol_date_seconds < time()

# EOL within 90 days
(xeol_eol_date_seconds - time()) / 86400 < 90

# past-EOL findings per workload: join on the image digest
sum by (namespace, kind, name) (
  xeol_image_workload * on(digest) group_left count by (digest) (xeol_eol_date_seconds < time())
)
```

The full metric list is in the [README](../README.md#metrics).

### JSON API

The same port serves read-only JSON, handy for scripts:

```bash
kubectl -n xeol-system port-forward svc/xeol-operator-metrics 8080
curl -s 'localhost:8080/api/v1/findings?within_days=0'          # already EOL
curl -s 'localhost:8080/api/v1/findings?namespace=prod&within_days=90'
curl -s localhost:8080/api/v1/status
```

Endpoints: `status`, `namespaces`, `workloads`, `findings`. Query parameters: `namespace` and `kind` (comma-separated) and `within_days` (default 180).

### AI assistants (MCP)

With `mcp.enabled`, the same four views are available as read-only MCP tools at `/mcp`. With the port-forward running:

```bash
claude mcp add --transport http xeol http://localhost:8080/mcp
```

Then ask, for example, "which workloads in prod run software that is already end of life?"

The web page, JSON API and MCP have no authentication. Reach them through `kubectl port-forward`; don't expose port 8080 through a public Ingress or LoadBalancer.

## 4. Choose what gets scanned

Scan settings live in Helm values under `scanner.*`; the chart writes them to the cluster-wide `XeolScanner` named `default`. Put them in a values file:

```yaml
# my-values.yaml
scanner:
  namespaceSelector:                 # only namespaces with this label; {} = all
    matchLabels: {xeol-scan: "true"}
  excludeNamespaces: ["kube-*", "monitoring"]
  excludeImages: ["*/busybox*"]      # globs on the image name
  workloadKinds: [Deployment, StatefulSet, DaemonSet]   # default also includes Job and CronJob
  workloadLabels: [team]             # adds a team label to metrics, for routing alerts
  interval: 24h                      # rescan each image this often
```

```bash
helm upgrade xeol-operator ./helm/xeol-operator -n xeol-system --reuse-values -f my-values.yaml
```

In globs, `*` matches anything (including `/`) and `?` matches one character.

Changes apply without a restart. If you manage the `XeolScanner` yourself (for example with GitOps), edit it directly instead, but note that a later `helm upgrade` writes it back from values.

## 5. Accept a known finding

Sometimes an EOL product is known and accepted, such as a test environment or software you can't replace yet. Hide it with `ignoreProducts`; the image is still scanned, but the finding disappears from metrics, alerts, the dashboard and the web page.

```yaml
scanner:
  ignoreProducts:
    - products: [perl]               # everywhere in namespace "test"
      namespaces: [test]
    - products: [python]             # only Python 3.8, only in the batch team's workloads
      cycles: ["3.8"]
      selector: {matchLabels: {team: batch}}
```

Products use their [endoflife.date](https://endoflife.date) name, e.g. `python`, `nodejs`, `debian`. The `url` on each finding links to the product's page, which shows the name.

## 6. Get alerted

With `prometheusRule.enabled`, these alerts are installed:

| Alert | Fires when |
|---|---|
| `XeolEOLSoftwareRunning` | A running image contains software past EOL. |
| `XeolEOLSoftwareSoon` | Software reaches EOL within `prometheusRule.eolSoonDays` (default 90). |
| `XeolDatabaseStale` | The EOL database is older than `prometheusRule.dbStaleDays` (default 30). |
| `XeolDatabaseMissing` | The operator has no EOL database. |
| `XeolScanErrors` | Images fail to scan. |

Route them in Alertmanager as usual. With `scanner.workloadLabels: [team]` you can join on `xeol_image_workload` to send each team its own findings.

## 7. Private registries

No extra setup. Each scan uses the pull secrets of the Pod running the image, and those of its ServiceAccount, exactly as the kubelet would.

## Troubleshooting

**A workload is missing.** Check that its namespace isn't excluded, that its top-level kind (e.g. Deployment, not ReplicaSet) is in `scanner.workloadKinds`, and that its image isn't excluded. Images loaded straight onto nodes, without a registry digest, can't be scanned and are skipped.

**No findings, but I know something is old.** Only products tracked by endoflife.date are detected. "No findings" means nothing known is EOL, not that nothing is.

**Failed scans.** `xeol_scan_errors_total` and the web page's failure tiles give the reason:

| Reason | Meaning | Fix |
|---|---|---|
| `auth` | The registry refused the pull. | Check the Pod's `imagePullSecrets`. |
| `not_found` | The image digest no longer exists in the registry. | Usually harmless; it goes away when the workload is redeployed. |
| `timeout` | The scan took longer than `scanner.timeout` (default 10m). | Raise `scanner.timeout`. |
| `oom` | The image needed more memory than the pod has. It is skipped for 24 hours. | Raise `resources.limits.memory` and `scanner.goMemLimit` together, e.g. `2Gi` and `1600MiB`. |
| `other` | Anything else. | See the operator's logs. |

**`DBStale` condition or `XeolDatabaseStale` alert.** The daily database rebuild is failing, usually because `endoflife.date` is unreachable. The operator keeps using the last good database meanwhile. Check egress, or switch to a mounted database ([air-gapped.md](air-gapped.md)).

**Pod not Ready.** It waits for the first database build. Check the logs for errors fetching `endoflife.date`.

## Upgrade and uninstall

```bash
git pull
kubectl apply --server-side -f helm/xeol-operator/crds/      # Helm never upgrades CRDs
helm upgrade xeol-operator ./helm/xeol-operator -n xeol-system --reuse-values
```

A new pod rescans everything from scratch.

```bash
helm uninstall xeol-operator -n xeol-system
kubectl delete crd xeolscanners.xeol.io                       # Helm leaves CRDs behind
```
