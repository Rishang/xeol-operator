## Why

The JSON API is only readable through Grafana with the Infinity plugin. Clusters without Grafana have no way to look at findings beyond raw `/metrics` and `curl`. A built-in HTML page and an MCP endpoint give the same data to a browser and to AI assistants, with nothing else installed.

## What Changes

- New HTML UI at `/` on the metrics port: summary, namespace/kind/window filters, findings, workloads and namespace tables. Server-rendered Go template, htmx from a pinned CDN for in-place updates, no build step.
- New MCP endpoint at `/mcp` (Streamable HTTP, stateless, read-only) with tools mirroring the JSON API: `status`, `namespaces`, `workloads`, `findings`.
- Operator flags `--ui` and `--mcp` (default off); Helm values `ui.enabled` and `mcp.enabled` (default false).
- NOTES.txt prints the port-forward URL for each when enabled.
- No new CRD field, metric or label.

## Non-goals

- Authentication, an Ingress, or TLS for the UI/MCP. Access is by `kubectl port-forward`, like the JSON API today.
- History or trends (the data is current state only, same as the API).
- Write actions (e.g. adding ignore rules from the UI or MCP).
- A JS framework or bundler. htmx (one pinned CDN script) is the only frontend dependency.

## Capabilities

### New Capabilities
- `web-ui`: built-in HTML page showing status, workloads and findings with filters.
- `mcp-server`: read-only MCP tools exposing the same data as the JSON API.

### Modified Capabilities
<!-- none: findings-export's JSON API is unchanged and reused -->

## Impact

- `internal/api`: shared row functions reused by the MCP tools; `ui.go` + embedded `ui/page.html` template.
- `cmd/main.go`: two flags, extra handlers mounted on the metrics server.
- `go.mod`: adds `github.com/modelcontextprotocol/go-sdk` (see design D2).
- Helm chart: `ui.enabled`, `mcp.enabled`, args in `deployment.yaml`, NOTES.txt.
- `AGENTS.md` Scope ("A custom UI ... out until asked for") updated: now asked for.
