# Proposal

## Why

With `trivy.enabled`, every vulnerability in every running image shows up in metrics, the dashboards and the web page. Teams accept some of them: a CVE in a package the workload never calls, a finding with no fix where the risk is understood, a known-noisy package. Today the only way out is `excludeImages`, which drops the image from scanning entirely (EOL findings too). EOL findings already have `ignoreProducts` for exactly this; vulnerabilities need the same.

## What Changes

- New config file field `ignoreVulnerabilities` (Helm `scanner.ignoreVulnerabilities`): rules that hide accepted vulnerabilities by CVE ID (`ids`) and/or package name (`packages`), optionally narrowed by `namespaces` globs and a `selector` on workload labels: the same scoping as `ignoreProducts`.
- Hidden at export time, like `ignoreProducts`: `xeol_vulnerabilities`, `/api/v1/vulnerabilities`, the vulnerability counts in status/workloads/namespaces, the web page's Trivy tab and the Grafana trivy dashboard (it reads the API). No rescan; removing a rule brings them back. Images are still scanned.
- Applies live through the existing config reload.
- AGENTS.md: vulnerability ignore rules move from "out of scope" to supported (VEX stays out).

## Non-goals

- VEX documents, `.trivyignore` files or passing ignores to trivy (results are filtered at export, so a rule change needs no rescan).
- Expiry dates on rules, severity-based rules ("hide all LOW"): use the severity filters of the API, page and dashboard.
- Ignoring by fixed/unfixed state.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities
- `findings-export`: accepted vulnerabilities can be hidden by config, like accepted EOL findings.
