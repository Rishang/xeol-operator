## ADDED Requirements

### Requirement: Ignore accepted vulnerabilities
`ignoreVulnerabilities` rules SHALL hide vulnerabilities from metrics, the API, status and the web page without rescanning. A rule matches by `ids` (vulnerability IDs) and/or `packages` (package names), case-insensitive, both must match when both are given, optionally narrowed by `namespaces` globs and a `selector` on workload labels. A vulnerability SHALL be dropped from metrics only when the rule applies to every workload running that digest.

#### Scenario: Ignore one CVE everywhere
- **WHEN** the config has `ignoreVulnerabilities: [{ids: [CVE-2024-1234]}]`
- **THEN** no `/api/v1/vulnerabilities` row has that ID, and the status, workload and metric counts leave it out

#### Scenario: Namespace-scoped rule on a shared digest
- **WHEN** `{packages: [libcrypto3], namespaces: [test]}` and a digest runs in `test` and `web`
- **THEN** the API hides libcrypto3 for the `test` workload only, and the metric still counts it

#### Scenario: Empty rule
- **WHEN** a rule has neither `ids` nor `packages`
- **THEN** the config file is rejected with an error naming the rule
