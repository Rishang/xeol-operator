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
`kubectl get xeolscanner default` showed IMAGES 9, EOL 3, Ready True.
("soon" = within 10 years here, from `/api/v1/workloads?within_days=3650`.)

## Grafana

- http://grafana.172.16.5.2.nip.io — login `admin` / `prom-operator`
- xeol dashboard: http://grafana.172.16.5.2.nip.io/d/xeol-operator
- The node IP (172.16.5.2) changes on a new cluster: `docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' k3d-k3s-default-server-0`

## Rebuild on a fresh cluster

```bash
k3d cluster create k3s-default            # keep traefik: Grafana's ingress uses it
NODE_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' k3d-k3s-default-server-0)

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm upgrade --install kube-prom-stack prometheus-community/kube-prometheus-stack --version 92.13.1 \
  --kube-context k3d-k3s-default -n monitoring --create-namespace -f - <<EOF
grafana:
  adminPassword: prom-operator
  ingress: {enabled: true, ingressClassName: traefik, path: /, hosts: [grafana.$NODE_IP.nip.io]}
  plugins: [yesoreyeram-infinity-datasource]
  sidecar:
    dashboards: {searchNamespace: ALL}
    datasources: {searchNamespace: ALL}
prometheus:
  prometheusSpec:
    podMonitorSelectorNilUsesHelmValues: false
    ruleSelectorNilUsesHelmValues: false
    serviceMonitorSelectorNilUsesHelmValues: false
EOF

kubectl --context k3d-k3s-default apply -f test/apps.yaml
task deploy-dev -- --set serviceMonitor.enabled=true,prometheusRule.enabled=true,grafanaDatasource.enabled=true,ui.enabled=true,mcp.enabled=true

# accepted findings, set on the CR directly (not in the Helm values)
kubectl --context k3d-k3s-default patch xeolscanner default --type merge -p \
  '{"spec":{"ignoreProducts":[{"products":["Gorilla Toolkit"],"namespaces":["monitoring"]},{"products":["perl"],"namespaces":["test"]}]}}'
```

Operator UI/API/MCP: `kubectl --context k3d-k3s-default -n xeol-system port-forward deploy/xeol-operator 8080`, then http://localhost:8080/.
Images loaded with `k3d image import` have no registry digest, so the operator itself is never scanned.
