# Proposal

## Why

The scan configuration is one cluster-scoped `XeolScanner/default` CR, which the Helm chart already renders from `scanner.*` values. The CRD costs more than it gives: installing it needs cluster-admin, Helm never upgrades CRDs (every upgrade needs a manual `kubectl apply` of the new CRD), and it pulls in kubebuilder codegen, a second controller and status writes. A YAML file from a ConfigMap, mounted into the pod and reloaded when it changes, carries the same settings with none of that.

## What Changes

- **BREAKING:** the `XeolScanner` CRD and `api/v1alpha1` are removed. The scan settings (the old `spec` fields, same names and meaning) live in a YAML file, `--config` (default `/etc/xeol-operator/config.yaml`), parsed strictly into a Go struct: unknown fields and bad values are errors.
- The chart renders `scanner.*` into a ConfigMap and mounts it (no `subPath`, so the kubelet keeps the file current). The operator re-reads the file every few seconds and applies a changed config without a restart: no pod reloader, no rollout.
- A bad file at startup stops the operator with the error. A bad edit later keeps the previous config, is logged, and shows as `config_error` in `/api/v1/status`. A missing file means built-in defaults.
- CR status (`imagesScanned`, `eolFindings`, `lastSweepTime`, `dbBuilt`, `Ready`, `DBStale`) goes away; the same facts are already in `/api/v1/status`, the metrics, the readiness probe and the alerts. `/api/v1/status` gains `last_sweep`.
- RBAC loses the `xeol.io` rules; nothing new is added (the file needs no API access).
- Taskfile, CI, kustomize `config/`, docs and AGENTS.md drop CRD generation and mention the config file instead. Upgrade note: after `helm upgrade`, `kubectl delete crd xeolscanners.xeol.io`.

## Non-goals

- Watching the ConfigMap through the API (instant, but needs RBAC and doesn't work from a local file); fsnotify (the kubelet's symlink swap makes a poll simpler and the kubelet delay dominates anyway).
- Moving pod-level flags (workers, memory, DB source, trivy) into the file: they still need a restart and stay flags.
- A config validation webhook or `check-config` command.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities
- `scanner-config`: configuration comes from a reloaded YAML file instead of `XeolScanner/default`; status moves to `/api/v1/status`.

## Impact

- Removed: `api/v1alpha1/`, `internal/controller/xeolscanner_controller.go` (+ test), `config/crd/`, `config/samples/`, XeolScanner RBAC roles in `config/rbac/`, `helm/xeol-operator/crds/`, `templates/xeolscanner.yaml`, PROJECT's resource entry, Taskfile `generate`/`chart-crds` and the CRD parts of `manifests`/`chart-check`/`run`/`deploy-dev`.
- New: `internal/config/` (struct, strict load, defaults, file watcher), `templates/configmap.yaml`.
- Changed: `cmd/main.go`, `internal/controller` (snapshot from the config struct, Pod re-evaluation trigger), `internal/api` (status), chart deployment (volume, `--config`), RBAC, envtest suite, docs (the CRD page becomes "Configuration"), README, AGENTS.md, `openspec/specs/scanner-config`.
- No metric or label changes.
