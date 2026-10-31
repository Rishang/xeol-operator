# Spec Delta

## MODIFIED Requirements

### Requirement: Config changes apply without restart
The operator SHALL notice a changed file within seconds of the kubelet updating it and apply it without a restart. A changed config SHALL re-evaluate every Pod; an unchanged one SHALL NOT. An invalid edit SHALL keep the previous configuration, be logged, and show as `config_error` in `/api/v1/status` until a valid file replaces it.

#### Scenario: New ignore rule
- **WHEN** an `ignoreProducts` rule is added to the ConfigMap
- **THEN** after the kubelet updates the file, metrics and the JSON API reflect it, with no rescan and no pod restart

#### Scenario: Bad edit
- **WHEN** the ConfigMap is edited to an invalid selector
- **THEN** scanning continues with the previous config and `/api/v1/status` has `config_error` set

### Requirement: Status
`/api/v1/status` SHALL report images scanned, findings, the EOL database's build time and age, failed scans, `last_sweep` (when every running image last had a completed scan) and `config_error`; readiness SHALL report whether the databases are usable.

#### Scenario: Stale DB
- **WHEN** the EOL database in use was built more than 30 days ago
- **THEN** `db_age_seconds` shows it, the `XeolDatabaseStale` alert fires, and scanning continues

## REMOVED Requirements

### Requirement: Singleton configuration
**Reason**: The `XeolScanner` CRD is removed; there is no CR, so no singleton, no `NotSingleton` condition and no "CR deleted" state.
**Migration**: Set the same fields under `scanner.*` in Helm values (unchanged), which the chart renders into the config file. After `helm upgrade`, run `kubectl delete crd xeolscanners.xeol.io` once.

## ADDED Requirements

### Requirement: Configuration file
The operator SHALL read its scan configuration (`namespaceSelector`, `excludeNamespaces`, `excludeImages`, `interval`, `workloadKinds`, `workloadLabels`, `ignoreProducts`) from one YAML file, `--config` (default `/etc/xeol-operator/config.yaml`), parsed strictly: an unknown field or bad value is an error. Pod-level settings SHALL stay operator flags.

#### Scenario: Rendered by Helm
- **WHEN** the chart is installed
- **THEN** `scanner.*` values are rendered into a ConfigMap, mounted at `/etc/xeol-operator/` without `subPath` so the kubelet keeps the file current

#### Scenario: Empty file
- **WHEN** the file is empty or missing
- **THEN** every namespace is selected, `interval` is `24h`, and `workloadKinds` is Deployment, StatefulSet, DaemonSet, Job, CronJob

#### Scenario: Every workload kind
- **WHEN** the file has `workloadKinds: []`
- **THEN** Pods of every kind are scanned

#### Scenario: Unknown field
- **WHEN** the file has `interval: 12h` and `intervall: 6h`
- **THEN** the file is rejected naming `intervall`

#### Scenario: Bad file at startup
- **WHEN** the operator starts with a file it can't parse
- **THEN** it exits with the parse error instead of scanning with a guess
