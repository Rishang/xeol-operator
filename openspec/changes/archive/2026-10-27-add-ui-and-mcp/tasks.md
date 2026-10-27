## 1. Shared API functions

- [x] 1.1 Split `internal/api` handlers into `func (h *Handler) Status/Namespaces/Workloads/Findings(q Query) (any, error)`-style functions used by HTTP; keep `/api/v1/*` responses identical. Check: `go test ./internal/api/...`

## 2. MCP

- [x] 2.1 Add `github.com/modelcontextprotocol/go-sdk`; `internal/api/mcp.go` registers the four tools (inputs `namespace`, `kind`, `within_days`) on a stateless Streamable HTTP handler, wrapped by the leader check. Check: `go test ./internal/api/...` with an in-process client listing tools and calling `findings`
- [x] 2.2 `--mcp` flag mounts `/mcp`; absent flag → 404. Check: test asserts both

## 3. UI

- [x] 3.1 `internal/api/ui/page.html` (Go template, embedded) + `/ui/report` fragment; htmx 2.0.4 from jsDelivr with SRI; filters, findings/workloads/namespaces tables, standby notice, light/dark. Check: `go test ./internal/api/` (TestUI, TestUIEscapes)
- [x] 3.2 `--ui` flag mounts exact `/`; absent flag → 404. Check: test asserts both

## 4. Helm and docs

- [x] 4.1 `ui.enabled`, `mcp.enabled` (default false) → `--ui`, `--mcp` args; NOTES.txt prints port-forward URLs. Check: `task chart-test` renders with both enabled
- [x] 4.2 `AGENTS.md` (Scope, Export decision, Layout), README section. Check: `rg -n 'mcp|ui.enabled' AGENTS.md README.md`

## 5. Verify on k3d

- [x] 5.1 `task deploy-dev -- --set ...,ui.enabled=true,mcp.enabled=true`; open the UI via port-forward and compare with `/api/v1/status`; `claude mcp add --transport http xeol http://localhost:8080/mcp` and call `findings`. Check: numbers match
