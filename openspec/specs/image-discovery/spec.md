# image-discovery Specification

## Purpose
Find the image digests actually running in the cluster and the workloads that run them.
## Requirements
### Requirement: Discover by running digest
The operator SHALL watch Pods in selected namespaces and take each container's (init containers included) digest from `status.containerStatuses[].imageID`, not the tag. Images are deduplicated by digest across the cluster.

#### Scenario: Same image in two workloads
- **WHEN** two Deployments run the same digest
- **THEN** it is scanned once and both workloads are attached to it

#### Scenario: No registry digest
- **WHEN** a container's `imageID` has no registry digest (not started yet, or a locally imported image)
- **THEN** that container is skipped

### Requirement: Namespace and image selection
A namespace SHALL be selected when it matches `namespaceSelector` (empty selects all) and no `excludeNamespaces` glob. An image SHALL be skipped when an `excludeImages` glob matches its Pod-spec reference or its repository.

#### Scenario: Excluded namespace
- **WHEN** `excludeNamespaces: ["kube-*"]`
- **THEN** Pods in `kube-system` are not tracked

### Requirement: Top-level workload
Each image SHALL be attributed to the Pod's top-level owner (Pod → ReplicaSet → Deployment, Job → CronJob, ...), or the Pod itself when it has none, with that workload's own labels.

#### Scenario: Deployment Pod
- **WHEN** a Pod is owned by a ReplicaSet owned by Deployment `api`
- **THEN** its images are attributed to `Deployment/api`

### Requirement: Pull credentials
Scans SHALL use the Pod's `imagePullSecrets` and its ServiceAccount's, written to a temporary `DOCKER_CONFIG` for the scan and deleted afterwards. Credentials SHALL never be logged.

#### Scenario: Unreadable pull secret
- **WHEN** a pull secret can't be read
- **THEN** the scan is counted as an error and not run

### Requirement: Removal
When no running Pod uses a digest any more, it SHALL drop out of the inventory, metrics and API. Its saved result SHALL be kept, so the image is restored rather than rescanned if it comes back.

#### Scenario: CronJob runs again
- **WHEN** a CronJob's next run uses the same digest within the rescan interval
- **THEN** its saved findings are restored with no rescan

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

