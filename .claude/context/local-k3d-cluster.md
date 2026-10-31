# Local test cluster (k3d) — state and rebuild recipe

Snapshot 2026-10-28. Context `k3d-k3s-default` (k3s v1.31, single node, traefik enabled for the Grafana ingress).
Always pass `--context k3d-k3s-default` / `--kube-context k3d-k3s-default`; the kubeconfig also holds prod EKS/AKS.

## Namespaces

| Namespace | What | Helm release |
|---|---|---|
| `test` | workloads with known-EOL images (`test/apps.yaml`) | — |
| `monitoring` | kube-prometheus-stack: Prometheus, Alertmanager, Grafana | `kube-prom-stack` (chart kube-prometheus-stack 92.13.1) |
| `xeol-system` | the operator | `xeol-operator` (local chart, image `xeol-operator:dev`) |

## Expected findings (namespace `test`)

| Workload | Image | EOL now | EOL soon |
|---|---|---|---|
| Deployment/node16 | node:16-alpine | 2 | 0 |
| Deployment/python38 | python:3.8-slim | 1 | 1 |
| DaemonSet/python38-agent | python:3.8-slim | 1 | 1 |
| Deployment/node22 | node:22-alpine | 0 | 2 |
| Deployment/python312 | python:3.12-slim | 0 | 2 |

`monitoring` adds EOL-soon findings only (grafana 3, kube-state-metrics 1, node-exporter 1).
`/api/v1/status` (port-forward 8080) showed 9 images scanned, 3 findings already EOL. (Before 2026-10-31 this was `kubectl get xeolscanner default`; the CRD is gone.)
("soon" = within 10 years here, from `/api/v1/workloads?within_days=3650`.)

## Grafana

- http://grafana.172.16.5.2.nip.io — login `admin` / `prom-operator`
- xeol dashboard: http://grafana.172.16.5.2.nip.io/d/xeol-operator
- The node IP (172.16.5.2) changes on a new cluster: `docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' k3d-k3s-default-server-0`

## Rebuild on a fresh cluster

```bash
task cluster-up      # idempotent: creates k3d-k3s-default if missing, then installs everything below
```

1. k3d cluster `k3s-default` (traefik kept: Grafana's ingress uses it; current context is not switched).
2. kube-prometheus-stack 92.13.1 in `monitoring` with `test/kube-prom-stack.values.yaml` (Infinity plugin, sidecars on all namespaces, selectors open); ingress host `grafana.<node-ip>.nip.io`.
3. `test/apps.yaml`: the EOL workloads in `test`.
4. `task deploy-dev -- -f test/dev-values.yaml`: operator with ServiceMonitor, PrometheusRule, Grafana datasource, UI, MCP, and the ignore rules (Gorilla Toolkit in `monitoring`, perl in `test`).

Redeploy only the operator after a code change: `task deploy-dev -- -f test/dev-values.yaml`.

Operator UI/API/MCP: `kubectl --context k3d-k3s-default -n xeol-system port-forward deploy/xeol-operator 8080`, then http://localhost:8080/.
Images loaded with `k3d image import` have no registry digest, so the operator itself is never scanned.
