# XeolScanner CRD

`XeolScanner` (`xeol.io/v1alpha1`, cluster-scoped) decides which running images are scanned, how often, and which workload labels end up on the metrics. The operator uses exactly one, named `default`. The Helm chart creates it from `scanner.*` values (see [helm.md](helm.md)); you can also manage it yourself.

How scans run (workers, memory, timeout, EOL database) is not part of the CRD. Those are operator flags set by Helm, because changing them needs a pod restart anyway.

## Spec

All fields are optional. `spec: {}` scans every running image in every namespace once a day.

| Field | Type | Default | Behaviour |
|---|---|---|---|
| `namespaceSelector` | LabelSelector | all namespaces | Only Pods in namespaces whose labels match are scanned. `matchLabels` and `matchExpressions` both work. |
| `excludeNamespaces` | []string (globs) | none | Namespaces to skip. Checked before `namespaceSelector`; an exclude always wins. |
| `excludeImages` | []string (globs) | none | Images to skip. Matched against the image as written in the Pod spec (`python:3.8-slim`) and against the resolved repository (`docker.io/library/python`). |
| `interval` | duration | `24h` | Time from a successful scan of a digest to its next scan. Failed scans retry after 1h; an image whose scan OOM-killed the operator waits 24h. Zero or negative means 24h. |
| `workloadKinds` | []string | `[Deployment, StatefulSet, DaemonSet, Job, CronJob]` | Top-level workload kinds to scan, matched exactly. A Pod's kind is its top owner (Pod → ReplicaSet → Deployment, Job → CronJob), so Pods of operator kinds such as `Prometheus` and `Alertmanager`, and bare Pods, are skipped. Set `[]` to scan every kind; add e.g. `Rollout` for Argo Rollouts. |
| `ignoreProducts` | []rule | none | Accepted findings to hide; see [Ignoring products](#ignoring-products). |
| `workloadLabels` | []string | none | Label keys copied from each top-level workload (Deployment, StatefulSet, CronJob, ...) onto `xeol_image_workload` as `k8s_label_<key>`. Characters other than `[a-zA-Z0-9_]` become `_`, so `app.kubernetes.io/part-of` becomes `k8s_label_app_kubernetes_io_part_of`. |

### What each field is for

- **`namespaceSelector`**: scan only what you opt in. Label the namespaces to scan (`kubectl label ns payments xeol-scan=true`) and select them, e.g. to roll out team by team or skip sandboxes.
- **`excludeNamespaces`**: skip namespaces you don't own or can't change, such as `kube-*`, `monitoring`, `cert-manager`. Their images are vendor-managed and would add findings nobody acts on.
- **`excludeImages`**: skip images that can't be scanned or don't matter: huge images that run out of memory, images from a registry the operator can't reach, `busybox`-style helpers.
- **`interval`**: how fresh results are versus how much scanning you do. An image's contents never change (it's scanned by digest), so rescans only pick up new EOL data; `24h` matches the daily database rebuild. Lower it only if you need new EOL dates sooner.
- **`workloadKinds`**: keep findings to the workloads your teams deploy. The default skips Pods owned by operators (`Prometheus`, `Alertmanager`, ...) and bare Pods. Add your own kinds, e.g. `Rollout` for Argo Rollouts, or `[]` for everything.
- **`ignoreProducts`**: accept a finding you've decided to live with, so it stops alerting and stops cluttering the dashboard, without excluding the image from scanning. Narrow it by cycle, namespace or workload labels.
- **`workloadLabels`**: route and group findings by ownership. `[team]` puts `k8s_label_team` on `xeol_image_workload`, so alerts can go to each team's channel and dashboards can group by team.

### Glob syntax

`excludeNamespaces`, `excludeImages` and `ignoreProducts[].namespaces` take globs where only two characters are special: `*` matches any run of characters, **`/` included**, and `?` matches one character. So `registry.internal/*` matches both `registry.internal/app` and `registry.internal/team/app`. The whole value must match: `kube-*` matches `kube-system` but not `my-kube-test`. Every string is a valid glob; anything else, such as `[` or `.`, matches itself.

### Changes apply live

Editing the CR re-evaluates every Pod in the cluster straight away; no restart. Newly excluded images drop out of the metrics on the next scrape, and newly included ones are queued for a scan. The operator logs `scan config applied` with the new values.

## Ignoring products

Some findings are known and accepted: a vendor image on an old Debian, a legacy service with a planned upgrade. `ignoreProducts` hides them from the metrics, alerts, JSON API, dashboard and `status.eolFindings`. Images are still scanned, and a removed rule brings its findings back on the next scrape with no rescan.

```yaml
spec:
  ignoreProducts:
    - products: [perl]                 # required: endoflife.date slug or display name, case-insensitive
      namespaces: [test]               # optional: namespace globs
    - products: [gorilla]
      selector:                        # optional: the workload's own labels
        matchLabels: {app.kubernetes.io/name: grafana}
    - products: [python]
      cycles: ["3.8"]                  # optional: only these release cycles
```

| Rule field | Meaning |
|---|---|
| `products` | The product's endoflife.date slug (the last part of its page: `nodejs`, `gorilla`, `rhel`) or its display name as shown on the dashboard (`Node.js`, `Gorilla Toolkit`, `Red Hat Enterprise Linux`). Case-insensitive; spaces inside a name are kept, so `[Gorilla Toolkit]` works as written. |
| `cycles` | Release cycles as shown in the `cycle` label (`3.8`, `12`). Empty means every cycle. |
| `namespaces` | Namespace name globs, same syntax as `excludeNamespaces`. Empty means every namespace. |
| `selector` | Label selector on the workload's own `metadata.labels`: the Deployment, DaemonSet, StatefulSet, CronJob (or a bare Pod's labels), not the Pod template's. Empty means every workload. |

A rule applies to a workload when both `namespaces` and `selector` match. A rule with neither applies everywhere.

**Images shared between workloads.** The EOL metric has one series per image digest, not per workload. A scoped rule hides the finding from the matching workloads' rows in the JSON API and dashboard tables, but `xeol_eol_date_seconds` (and the alerts built on it) only drops it when every workload running that image matches. Example: `python:3.8-slim` running in `test/python38` and `prod/api` with `{products: [python], namespaces: [test]}` stays in the metric and still alerts, because of `prod/api`.

An invalid selector sets `Ready=False, InvalidSpec`, and the previous config stays in effect.

## Status

Updated by the operator on every change and at least once a minute. Use it for a quick health check without Prometheus or Grafana (`kubectl get xeolscanner`), or in GitOps tools that read conditions: `Ready` tells you scanning works, `DBStale` that the EOL data needs refreshing.

| Field | Meaning |
|---|---|
| `imagesScanned` | Running image digests that have a scan result (success or failure). |
| `eolFindings` | Findings already past their EOL date, not counting ignored ones. Upcoming EOL dates are in the metrics, not here. |
| `lastSweepTime` | Set once every running digest has a result: the time of the oldest one. |
| `dbBuilt` | Build time of the EOL database in use. |
| `conditions` | `Ready` and `DBStale`, below. |

| Condition | Status | Reason | Meaning |
|---|---|---|---|
| `Ready` | True | `Scanning` | Config applied, EOL database ready. |
| `Ready` | False | `DBNotReady` | The first database build hasn't finished (online), or the mounted database is missing. |
| `Ready` | False | `InvalidSpec` | `namespaceSelector` or an `ignoreProducts` selector can't be parsed. The previous config stays in effect. |
| `Ready` | False | `NotSingleton` | This CR isn't named `default` and is ignored. |
| `DBStale` | False | `Fresh` | Database built within 30 days. |
| `DBStale` | True | `OlderThan30Days` | Scans continue; refresh the database (see [air-gapped.md](air-gapped.md)). |
| `DBStale` | Unknown | `NoDatabase` | No database yet. |

`Ready` only reflects the database. If scans are failing (for example registry auth), watch `xeol_scan_errors_total` or the `XeolScanErrors` alert.

```console
$ kubectl get xeolscanner
NAME      IMAGES   EOL   DB BUILT   READY
default   42       7     5h         True
```

## Lifecycle

- **No `default` CR:** nothing is scanned. The operator still builds the EOL database.
- **Deleted:** scanning stops and all finding series disappear on the next scrape. Saved results stay on the pod's state volume, so recreating the CR restores them instead of rescanning.
- **Any other name:** accepted by the API server, then marked `Ready=False, NotSingleton` and never used.

## Examples

Everything, daily:

```yaml
apiVersion: xeol.io/v1alpha1
kind: XeolScanner
metadata:
  name: default
spec: {}
```

Opt-in namespaces, team label on metrics, twice a day:

```yaml
apiVersion: xeol.io/v1alpha1
kind: XeolScanner
metadata:
  name: default
spec:
  namespaceSelector:
    matchLabels: {xeol-scan: "true"}     # kubectl label ns payments xeol-scan=true
  workloadLabels: [team]
  interval: 12h
```

Skip system namespaces and internal images:

```yaml
apiVersion: xeol.io/v1alpha1
kind: XeolScanner
metadata:
  name: default
spec:
  excludeNamespaces: ["kube-*", "monitoring", "cert-manager"]
  excludeImages:
    - "registry.internal/*"              # registry.internal/app and registry.internal/team/app
    - "docker.io/library/busybox"
```

## Upgrading the CRD

Helm installs `crds/` once and never upgrades it. Before `helm upgrade` to a chart version with CRD changes:

```bash
kubectl apply --server-side -f helm/xeol-operator/crds/
```

Without this, new fields are dropped by the API server.
