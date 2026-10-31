# Tasks

## 1. Rescan

- [x] 1.1 Inventory match by image globs and a rescan function that queues the matches (D1); verify a controller/scanner test: matching globs, all images, nothing matched, already-queued digest not duplicated
- [x] 1.2 Admin listener `POST /rescan` on `--admin-bind-address` (D2), 503 on standby, size caps; verify `go test ./internal/api/`
- [x] 1.3 `/manager rescan` CLI (D4); verify a test against an httptest server
- [x] 1.4 MCP `rescan` tool behind `--mcp-allow-rescan` (D3); verify the tool is listed only with the flag and queues images

## 2. Chart and docs

- [x] 2.1 Helm `mcp.allowRescan`; verify `task chart-test`
- [x] 2.2 Docs (Usage: rescan now; Helm values; Architecture), AGENTS.md (scope, flags); verify `task docs-build`
- [x] 2.3 `openspec validate add-rescan-now --strict`, `task lint`, `task test`

## 3. Verify on k3d

- [x] 3.1 `kubectl exec ... /manager rescan python:3.8*` queues only the python 3.8 images and they are rescanned; with `mcp.allowRescan` the MCP tool does the same
