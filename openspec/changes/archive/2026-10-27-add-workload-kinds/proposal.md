## Why

Operator-managed kinds such as Prometheus and Alertmanager show up as workloads and get scanned, but they are platform components the team doesn't own. Scanning should focus on the standard workload kinds.

## What Changes

- New CRD field `spec.workloadKinds`: top-level workload kinds to scan, default `[Deployment, StatefulSet, DaemonSet, Job, CronJob]`. Pods whose top-level owner has another kind (Prometheus, Alertmanager, a bare Pod, ...) are not tracked. An explicit `[]` scans every kind.
- Helm value `scanner.workloadKinds` (empty = CRD default).
- No new metric or label.

## Non-goals

- Excluding by kind per namespace, or kind globs.
- A separate `excludeKinds`: the allowlist covers it.

## Capabilities

### New Capabilities

### Modified Capabilities
- `image-discovery`: workloads are filtered by top-level kind.

## Impact

- `api/v1alpha1` (new field, CRD regenerated, chart CRD copy), `internal/controller` (snapshot, Pod reconcile, change detection), chart values/template, `docs/crd.md`, `AGENTS.md`.
- Existing clusters: after upgrade, workloads of other kinds drop out of metrics, the API and the UI.
