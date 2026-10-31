# Tasks

## 1. Config file

- [x] 1.1 `internal/config`: `Config`/`IgnoreRule` (old spec fields and tags), `Parse` (strict, defaults per D1), `Load` (missing = empty); verify `go test ./internal/config/` covers defaults, `workloadKinds: []`, unknown field, bad duration
- [x] 1.2 `config.Watcher` (D2): poll, apply changed bytes through a callback, report errors; verify a test that rewrites the file (also via a `..data` symlink swap) sees the change and keeps the old config on a bad file

## 2. Operator

- [x] 2.1 `controller.SnapshotFrom(*config.Config)`, ignore rules on `config.IgnoreRule`, `Config` gains the last load error; delete `XeolScannerReconciler`; verify `go build ./...`
- [x] 2.2 `cmd/main.go`: `--config` flag, load at startup (exit on error), apply + `Changed` signal on reload, add the watcher; drop the CRD scheme and controller; verify `go vet ./...`
- [x] 2.3 `/api/v1/status`: `last_sweep` and `config_error` (D4); verify `go test ./internal/api/`
- [x] 2.4 Tests off `api/v1alpha1`; envtest without CRDs; verify `task test` and `task lint`

## 3. Chart and repo cleanup

- [x] 3.1 Chart: `templates/configmap.yaml`, mount and `--config` in the Deployment, delete `crds/` and `templates/xeolscanner.yaml`, RBAC without `xeol.io`, NOTES.txt; verify `helm lint`, `task chart-test`, and `helm template` shows the ConfigMap and mount (no `subPath`)
- [x] 3.2 Delete `api/`, `config/crd`, `config/samples`, XeolScanner roles, PROJECT resource; Taskfile without CRD steps (D6); verify `task manifests` regenerates `config/rbac/role.yaml` without `xeol.io` and `go mod tidy && git diff --exit-code go.mod`

## 4. Verify on k3d

- [x] 4.1 `task deploy-dev -- -f test/dev-values.yaml --set trivy.enabled=true`; verify Ready, all images scanned, findings equal to before
- [x] 4.2 Edit the ConfigMap (add an `ignoreProducts` rule) with `kubectl edit`; verify the finding disappears from `/api/v1/findings` without a pod restart (same pod name, restart count 0), then a bad edit keeps it working and sets `config_error`, then revert

## 5. Docs and specs

- [x] 5.1 Docs site: the CRD page becomes "Configuration" (file fields, globs, ignore rules, reload, errors); Usage, Helm, Quick start, Architecture, Air-gapped drop `XeolScanner`/`kubectl get xeolscanner`; upgrade note to delete the old CRD; verify `task docs-build`
- [x] 5.2 README and AGENTS.md (API section, State, Layout, conventions); verify `rg -n 'XeolScanner|xeolscanner' --glob '!openspec/changes/archive/**' --glob '!openspec/changes/replace-crd-with-config-file/**'` finds only the upgrade notes
- [x] 5.3 `openspec validate replace-crd-with-config-file --strict`
