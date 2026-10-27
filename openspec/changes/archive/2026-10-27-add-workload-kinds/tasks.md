## 1. API and controller

- [x] 1.1 `WorkloadKinds []string` with CRD default; `task manifests generate chart-crds`. Check: `task chart-check`
- [x] 1.2 Snapshot + change detection + skip in Pod reconcile. Check: envtest for a Prometheus-owned Pod (skipped), a Deployment Pod (tracked), `[]` (all)

## 2. Chart and docs

- [x] 2.1 `scanner.workloadKinds` value and template; `docs/crd.md`, `AGENTS.md`. Check: `task chart-test`

## 3. Verify on k3d

- [x] 3.1 Redeploy; Alertmanager and Prometheus gone from `/api/v1/workloads`. Check: `kubectl get --raw .../api/v1/workloads`
