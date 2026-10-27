# scanner-config Specification

## Purpose
Configure scanning through one cluster-scoped `XeolScanner` named `default`, and report the operator's state on it.

## Requirements

### Requirement: Singleton configuration
The operator SHALL read its scan configuration only from `XeolScanner/default`. Pod-level settings (workers, memory, DB source, directories) SHALL be operator flags, not CR fields.

#### Scenario: Other names are ignored
- **WHEN** an `XeolScanner` with another name exists
- **THEN** it has no effect and gets `Ready=False`, reason `NotSingleton`

#### Scenario: Default deleted
- **WHEN** `XeolScanner/default` is deleted
- **THEN** scanning stops and the inventory is cleared, so no metrics are exported

#### Scenario: Empty spec
- **WHEN** `XeolScanner/default` is created with an empty spec
- **THEN** every namespace is selected and `interval` defaults to `24h`

### Requirement: Glob matching
`excludeNamespaces`, `excludeImages` and `ignoreProducts[].namespaces` SHALL match whole strings, where only `*` (any characters, `/` included) and `?` (one character) are special. Every string SHALL be a valid glob.

#### Scenario: Image glob crosses path segments
- **WHEN** `excludeImages: ["registry.internal/*"]`
- **THEN** `registry.internal/team/app:1.2` is excluded and `docker.io/app` is not

#### Scenario: Brackets are literal
- **WHEN** a glob contains `[ab]`
- **THEN** it matches those characters literally and the spec stays valid

### Requirement: Config changes apply without restart
A spec change SHALL re-evaluate every Pod; an unchanged spec SHALL NOT. An invalid spec (e.g. a bad label selector) SHALL set `Ready=False`, reason `InvalidSpec`, and keep the previous configuration.

#### Scenario: New ignore rule
- **WHEN** an `ignoreProducts` rule is added
- **THEN** status, metrics and the JSON API reflect it on the next reconcile, with no rescan

### Requirement: Status
The operator SHALL refresh status every minute with `imagesScanned`, `eolFindings` (findings already past EOL, after ignore rules), `lastSweepTime`, `dbBuilt`, and conditions `Ready` (`DBNotReady` while the DB is unusable) and `DBStale` (DB older than 30 days).

#### Scenario: Stale DB
- **WHEN** the DB in use was built more than 30 days ago
- **THEN** `DBStale=True` and scanning continues
