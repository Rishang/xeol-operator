## Context

`internal/api.Handler` serves `/api/v1/{status,namespaces,workloads,findings}` via `ExtraHandlers` on controller-runtime's metrics server (port 8080, `--metrics-secure=false` in the chart). Each request builds its answer from one inventory snapshot, and non-leader replicas answer 503. The Grafana dashboard is the only consumer today.

## Goals / Non-Goals

**Goals:** see the same data without Grafana, in a browser or from an MCP client; off by default; no new process, port or Service.

**Non-Goals:** auth, Ingress, history, write actions, frontend tooling.

## Decisions

**D1. Same port, same handler map.** Mount `/` (UI) and `/mcp` next to `/api/v1/*` in `ExtraHandlers`, only when the flag is set. Alternative: a separate port/Service; rejected, it adds Service, probe and NetworkPolicy surface for no gain. `/` matches only the exact path; anything else not registered stays 404.

**D2. MCP via the official Go SDK (`github.com/modelcontextprotocol/go-sdk`), stateless Streamable HTTP.** `mcp.AddTool` with typed inputs derives the JSON schemas; the SDK handles protocol version negotiation, batching and errors. Alternative: hand-roll JSON-RPC (`initialize`, `tools/list`, `tools/call`, ~100 lines, no dependency); rejected as the default because protocol edge cases (version negotiation, notifications, error shapes) are exactly what an SDK gets right and a hand-roll drifts on.

**D3. MCP tools reuse the API's functions.** Factor the four handlers' bodies so each takes a `query` and returns the value; HTTP and MCP both call them. Tool inputs: `namespace`, `kind` (comma-separated strings) and `within_days` (int), same meaning and defaults as the query string. Output: the same JSON as the API, as structured content plus text.

**D4. UI is server-rendered with htmx** (decided 2026-10-27). `ui/page.html` is a Go `html/template` (`//go:embed`): every cluster value is escaped on the server, and tests check the HTML. htmx 2.0.4 comes from jsDelivr, pinned with SRI and allowed by the CSP; the browser loads it, so an air-gapped cluster is fine whenever the viewer has internet, and without it the filters form is a plain GET that reloads the page. Filter changes and the Refresh button swap in `/ui/report`. Alternatives: a static page with vanilla JS on `/api/v1/*` (built, then dropped for client-side escaping and no Go tests of the output); vendoring htmx (rejected: a 51 KB file to keep current).

**D5. Leader handling unchanged.** Non-leaders answer 503 on `/api/v1/*` and `/mcp`; the UI renders a standby notice instead. With one replica (the default) this never happens.

## Risks / Trade-offs

- [No auth on the port] → Same exposure as the existing JSON API: read-only EOL data, ClusterIP only, off by default. Document that it must not be exposed through an Ingress without auth.
- [New dependency] → One module, official, maintained by the MCP org; pinned in `go.mod`. If it proves heavy, D2's hand-rolled alternative fits behind the same `/mcp` path.
- [Port-forward to a Service with 2 replicas may hit a standby] → 503 with a clear message; users port-forward the Deployment instead (`kubectl port-forward deploy/xeol-operator 8080`).

## Migration Plan

Additive, default off. Rollback: set the values back to false.

## Open Questions

None blocking.
