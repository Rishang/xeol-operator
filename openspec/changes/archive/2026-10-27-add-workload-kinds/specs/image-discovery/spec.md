## ADDED Requirements

### Requirement: Workload kind allowlist
The operator SHALL track a Pod only when its top-level workload kind is in `spec.workloadKinds` (default `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `CronJob`; exact, case-sensitive). An empty list SHALL mean every kind. A change to the list SHALL re-evaluate every Pod.

#### Scenario: Operator-managed kind
- **WHEN** a Pod's top-level owner is a `Prometheus` and `workloadKinds` is the default
- **THEN** its images are not tracked, scanned or exported

#### Scenario: Job from a CronJob
- **WHEN** a Pod is owned by a Job owned by a CronJob
- **THEN** it is tracked as the CronJob

#### Scenario: Every kind
- **WHEN** `workloadKinds: []`
- **THEN** Pods of any top-level kind, bare Pods included, are tracked
