# xeol-operator

Scans every container image running in a Kubernetes cluster for end-of-life (EOL) software, such as Python 3.8, Node 16 or Alpine 3.18, and reports the findings to Prometheus, Grafana, a built-in web page and MCP tools. With trivy enabled, the same scan also lists known vulnerabilities (CVEs).

- Source and issues: https://github.com/Rishang/xeol-operator
- Documentation: https://rishang.github.io/xeol-operator/
- Image: `docker.io/rishang/xeol-operator`

## How it works

The operator watches Pods and keeps an inventory of the image digests they run. It scans each digest once with syft to build an SBOM, then matches that SBOM with xeol (EOL software, against a database built daily from [endoflife.date](https://endoflife.date)) and, if enabled, with trivy (vulnerabilities). Each digest is rescanned every `scanner.interval` (default 24h). It runs as one read-only pod with no database or extra services.

## Install

```bash
helm repo add xeol-operator https://rishang.github.io/xeol-operator
helm repo update

helm install xeol-operator xeol-operator/xeol-operator -n xeol-system --create-namespace
```

With kube-prometheus-stack and Grafana (needs the Infinity plugin `yesoreyeram-infinity-datasource`):

```bash
helm install xeol-operator xeol-operator/xeol-operator -n xeol-system --create-namespace \
  --set serviceMonitor.enabled=true \
  --set prometheusRule.enabled=true \
  --set grafanaDatasource.enabled=true
```

Without Grafana, use the built-in web page and MCP endpoint:

```bash
helm install xeol-operator xeol-operator/xeol-operator -n xeol-system --create-namespace \
  --set ui.enabled=true --set mcp.enabled=true
kubectl -n xeol-system port-forward deploy/xeol-operator 8080
# open http://localhost:8080/
```

The web page, MCP tools and JSON API are read-only and have no authentication. Reach them with `port-forward` and do not expose them through a public Ingress.

## Vulnerability scanning (optional)

```bash
helm upgrade xeol-operator xeol-operator/xeol-operator -n xeol-system --reuse-values --set trivy.enabled=true
```

trivy reads the SBOM that syft already built, so images are not pulled twice. The pod downloads trivy's database (about 1.6 GB) at start and daily, and becomes Ready once it has it. For air-gapped clusters, mount the database instead: see [air-gapped installation](https://rishang.github.io/xeol-operator/air-gapped/). To scan only for vulnerabilities, add `--set eol.enabled=false`. At least one scan type must stay enabled.

## Common values

| Value | Default | Purpose |
|---|---|---|
| `eol.enabled` | `true` | Scan for end-of-life software with xeol |
| `trivy.enabled` | `false` | Scan for known vulnerabilities with trivy |
| `scanner.interval` | `24h` | How often each image digest is rescanned |
| `scanner.namespaceSelector` | `{}` | Only scan namespaces matching this selector (empty = all) |
| `scanner.excludeNamespaces` | `["kube-*"]` | Namespace globs to skip |
| `scanner.excludeImages` | `[]` | Image reference globs to skip |
| `scanner.ignoreProducts` | `[]` | Accepted EOL findings, hidden from metrics, API and status |
| `scanner.ignoreVulnerabilities` | `[]` | Accepted vulnerabilities, hidden the same way |
| `db.source` | `online` | `online` builds the EOL database from endoflife.date; `mounted` reads a prebuilt one |
| `ui.enabled` | `false` | Built-in web page at `/` |
| `mcp.enabled` | `false` | MCP endpoint at `/mcp` for AI assistants |
| `serviceMonitor.enabled` | `false` | ServiceMonitor for the Prometheus Operator |
| `prometheusRule.enabled` | `false` | Alert rules (EOL software, stale databases, scan errors) |
| `grafanaDatasource.enabled` | `false` | Infinity datasource and dashboards for Grafana sidecars |
| `resources` | 100m / 256Mi request, 1Gi limit | Pod resources. Keep `scanner.goMemLimit` below the memory limit |

All options are documented in the chart's `values.yaml` and in the [configuration guide](https://rishang.github.io/xeol-operator/configuration/).

## Uninstall

```bash
helm uninstall xeol-operator -n xeol-system
```
