# Design

## D1: Rule shape

```yaml
ignoreVulnerabilities:
  - ids: [CVE-2024-1234]          # trivy's vulnerability IDs (CVE-..., GHSA-...), case-insensitive
    packages: [libcrypto3]        # package names as trivy reports them, case-insensitive
    namespaces: [test]            # optional globs
    selector: {matchLabels: {team: legacy}}   # optional, the workload's own labels
```

A rule needs `ids`, `packages` or both (an empty rule is a config error, like `ignoreProducts` without `products`). With both, a vulnerability must match both (that CVE in that package). `namespaces` and `selector` work exactly as in `ignoreProducts`: a rule applies to a workload only when both match; with neither it applies everywhere.

## D2: Where it applies

Same two views as `ignoreProducts`:
- Per workload (`/api/v1/vulnerabilities` rows, workload and namespace counts, status workload counts, the Trivy tab table): hidden when a rule applies to that workload.
- Per image (`xeol_vulnerabilities`, status `by_severity`, `images_vulnerable`, `fixable_critical_high`): the metric is per digest, so a vulnerability is dropped only when a global rule matches or a rule applies to every workload running the digest.

Both are one function each on the config snapshot that filters an image's vulnerability list; every consumer counts or lists what they return (the shared `scanner.CountVulns` tally does the counting).

## D3: Matching code

`ignoreProducts` and `ignoreVulnerabilities` share the scope part (namespaces, selector, "global", "every workload") in `internal/controller/ignore.go`; only the match on the finding differs.
