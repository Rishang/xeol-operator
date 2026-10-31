# Design

## Context

- `api/v1alpha1.XeolScannerSpec` holds the scan settings; `XeolScannerReconciler` reads `XeolScanner/default`, turns it into `controller.ConfigSnapshot` (`SnapshotFrom`), stores it in the shared `controller.Config`, signals `Config.Changed` (the Pod controller then re-evaluates every Pod), and writes status every minute.
- Consumers read only `controller.Config` / `ConfigSnapshot`: the Pod controller, scan store, collector, API. They don't touch the CR.
- The chart renders `templates/xeolscanner.yaml` from `scanner.*` and ships the CRD in `crds/`. Defaults that matter: `interval` 24h (Go), `workloadKinds` (CRD `+kubebuilder:default`), `excludeNamespaces: [kube-*]` (chart values).

## Goals / Non-Goals

**Goals:**
- Same settings, same YAML field names, same meaning; only the carrier changes.
- Edits apply without a pod restart; bad edits never replace a good config.
- Delete the CRD machinery (types, codegen, CRD files, status writer, RBAC).

**Non-Goals:**
- API watch of the ConfigMap, fsnotify, webhooks (see proposal).
- New settings.

## Decisions

### D1. `internal/config`: the struct, strict load, defaults
`config.Config` is the old spec with the same JSON tags (`namespaceSelector` as `*metav1.LabelSelector`, `interval` as `metav1.Duration`, `ignoreProducts []config.IgnoreRule`). `config.Parse([]byte)` uses `sigs.k8s.io/yaml.UnmarshalStrict`, so unknown fields fail, then applies defaults the CRD used to apply: `interval` 24h when zero, `workloadKinds` = Deployment, StatefulSet, DaemonSet, Job, CronJob when absent (nil) while an explicit `[]` stays empty = every kind. `config.Load(path)` treats a missing file as an empty one. Validation that needs compiling (label selectors, ignore rules) stays in `controller.SnapshotFrom`, which now takes `*config.Config`; so "valid" means "Parse and SnapshotFrom both succeed".

### D2. Reload by polling the file
`config.Watcher` (a `manager.Runnable`, `NeedLeaderElection` false: every replica needs it) reads the file every 5 s and, when the bytes differ from the last applied ones, parses and applies it through a callback. A ConfigMap volume is updated by the kubelet with an atomic `..data` symlink swap; reading the path each time follows it with no special handling. The kubelet's own sync delay (up to about a minute) dominates the 5 s.
- *Alternative: fsnotify on the directory.* Needs to handle the symlink swap's events and re-adding watches; no faster in practice. Rejected.
- *Alternative: watch the ConfigMap via the API.* Instant, but needs RBAC and doesn't work from a local file. Rejected by the user in favour of the mounted file.

### D3. Startup and bad edits
`main` loads the file before starting the manager: a parse or compile error exits with it (nothing to fall back to). Later, a bad file keeps the current snapshot, logs the error once per distinct content, and sets `controller.Config`'s error, which `/api/v1/status` reports as `config_error` (cleared by the next good file). `Config.Set` and the `Changed` signal work as before, so the Pod re-evaluation path is unchanged; the event's object becomes a placeholder ConfigMap.

### D4. Status moves to the API
The CR status fields already exist in `/api/v1/status` (images scanned, findings, DB built/age, failed scans) except the last sweep, which is added as `last_sweep` (RFC 3339, empty until every running image has a result). Readiness keeps reporting the databases; `XeolDatabaseStale` keeps covering DB age.

### D5. Chart
`templates/configmap.yaml` renders `config.yaml` from `scanner.*` (same keys as before); the Deployment mounts it at `/etc/xeol-operator` (no `subPath`) and passes `--config=/etc/xeol-operator/config.yaml`. No checksum annotation: a config change must not roll the pod. `crds/` and `templates/xeolscanner.yaml` are deleted; RBAC drops the `xeol.io` rules. NOTES.txt and docs say to delete the old CRD after upgrading from a chart that had it.

### D6. Repo cleanup
Delete `api/`, `config/crd`, `config/samples`, XeolScanner roles in `config/rbac`, PROJECT's resource entry, Taskfile `generate` and `chart-crds`, the CRD parts of `manifests`, `chart-check`, `run`, `deploy-dev`; envtest starts without CRDs. `config/rbac/role.yaml` stays generated from the remaining `+kubebuilder:rbac` markers.

## Risks / Trade-offs

- [No API-server validation on apply] → strict parsing with clear errors, previous config kept, `config_error` in `/api/v1/status`, and `helm template` renders from typed values anyway.
- [Kubelet delay of up to ~1 min] → acceptable for scan settings; documented.
- [Upgrading a chart that had the CRD] → Helm removes the `XeolScanner/default` object (its template is gone) but leaves the CRD: one documented `kubectl delete crd`. Nothing else reads the CR.
- [`kubectl get xeolscanner` habit] → docs point to `/api/v1/status` and the web page.
