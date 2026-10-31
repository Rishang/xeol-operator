# Tasks

## 1. Config and matching

- [x] 1.1 `config.VulnIgnoreRule` and `Config.IgnoreVulnerabilities`; `Parse` rejects a rule with neither `ids` nor `packages`; verify `go test ./internal/config/`
- [x] 1.2 Compile into the config snapshot (change detection included); shared scope matching with `ignoreProducts` (D3); `VulnsFor(vulns, workload)` and `ImageVulns(vulns, workloads)` (D2); verify `go test ./internal/controller/` covers id, package, both, namespace-scoped rule on a shared digest

## 2. Export

- [x] 2.1 Metrics collector and API (rows, workload/namespace counts, status, Trivy tab) use the filtered lists; verify `go test ./internal/api/ ./internal/metrics/` with a rule hiding one CVE

## 3. Chart and docs

- [x] 3.1 Chart: `scanner.ignoreVulnerabilities` rendered into the config file; verify `task chart-test`
- [x] 3.2 Docs (Configuration: section, fields table, full example; trivy notes), AGENTS.md scope and config example; verify `task docs-build`
- [x] 3.3 `openspec validate add-vuln-ignore-rules --strict`, `task lint`, `task test`

## 4. Verify on k3d

- [x] 4.1 With trivy on, add a rule for one CVE to the ConfigMap; verify it disappears from `/api/v1/vulnerabilities` and the counts drop, without a restart
