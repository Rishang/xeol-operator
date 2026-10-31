> **Status: future scope, not approved.** Recorded 2026-10-27 after a security review flagged it. Today's decision: leave it, fine for a single-team cluster or one where everything running in it is trusted. Revisit before running in a shared multi-team cluster.

## Why

The metrics port (8080) serves `/metrics`, the JSON API (`/api/v1/*`), and, when enabled, the UI (`/`) and MCP (`/mcp`) over plain HTTP with no authentication. Any pod that can reach the port can read every namespace's findings: which workloads run which end-of-life software, with image, product and version. In a shared cluster a team limited to its own namespace can read everyone else's, and an attacker inside the cluster gets a list of outdated software to target.

`/metrics` already exposes the same data (the `xeol_image_workload` join gives namespace and workload), so the API, UI and MCP add no new data; any fix has to cover the whole port.

## What Changes

Options, cheapest first; pick when this is taken up:

1. **NetworkPolicy (chart, optional):** allow ingress to 8080 only from Prometheus and Grafana (namespace/pod selectors as values). `kubectl port-forward` still works. Blocks "any pod can read it"; no per-user scoping.
2. **Kubernetes authn/authz on the port:** TokenReview + SubjectAccessReview per request (controller-runtime `filters.WithAuthenticationAndAuthorization`, HTTPS). Prometheus and the Grafana Infinity datasource then need a ServiceAccount token; the UI behind port-forward needs a token too.
3. **Per-namespace results:** on top of 2, filter API/UI/MCP results to namespaces the caller may `list pods` in. `/metrics` stays all-or-nothing.

## Non-goals

- User login, sessions or an identity provider for the UI.
- Ingress or public exposure of the UI/MCP.
- Write actions.

## Capabilities

### New Capabilities
- `api-access`: who may reach the metrics/API port and what they may see.

### Modified Capabilities
- `findings-export`, `web-ui`, `mcp-server`: requests may require a token; results may be scoped by namespace.

## Impact

- Option 1: chart only (`templates/networkpolicy.yaml`, values, `ui/src/content/docs/helm.mdx`).
- Options 2-3: `cmd/main.go` (secure serving), `internal/api`, RBAC (`tokenreviews`, `subjectaccessreviews` create: beyond today's read-only RBAC), ServiceMonitor (bearer token, TLS), Grafana datasource (token), docs. Breaking for existing Grafana/Prometheus setups.
