## ADDED Requirements

### Requirement: On-demand rescan
The operator SHALL queue an immediate rescan of every running image, or of those whose image (as written in the Pod spec, or its repository) matches given globs, when asked through `/manager rescan` inside the pod. The request SHALL reach the operator only over a loopback listener. Rescans SHALL go through the scan queue, so a digest is never scanned twice at once, OOM backoff still applies, and the databases are not refreshed.

#### Scenario: Rescan one image family
- **WHEN** `kubectl exec deploy/xeol-operator -- /manager rescan 'python:3.8*'` runs
- **THEN** the digests of `python:3.8-slim` are queued and rescanned, and the command prints them

#### Scenario: Nothing matches
- **WHEN** the globs match no running image
- **THEN** nothing is queued and the command says no running image matches

#### Scenario: Repeated request
- **WHEN** a rescan is requested twice while the first is still queued
- **THEN** each digest is scanned once
