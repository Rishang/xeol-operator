# mcp-server Specification

## Purpose
TBD - created by archiving change add-ui-and-mcp. Update Purpose after archive.
## Requirements
### Requirement: MCP endpoint
When started with `--mcp` (Helm `mcp.enabled=true`), the operator SHALL serve a stateless Streamable HTTP MCP server at `/mcp` on the metrics port, with read-only tools `status`, `namespaces`, `workloads` and `findings`. Without `--mcp`, `/mcp` SHALL return 404.

#### Scenario: List tools
- **WHEN** an MCP client initializes against `http://localhost:8080/mcp` and lists tools
- **THEN** it gets `status`, `namespaces`, `workloads` and `findings`, each with an input schema

### Requirement: Same data as the JSON API
Each tool SHALL take optional `namespace` and `kind` (comma-separated) and `within_days` (default 180), and return the same JSON as the matching `/api/v1/*` endpoint, ignore rules applied.

#### Scenario: Findings tool
- **WHEN** a client calls `findings` with `within_days: 0`
- **THEN** the result equals `GET /api/v1/findings?within_days=0`

#### Scenario: Invalid input
- **WHEN** `within_days` is negative
- **THEN** the tool returns an error result, not a protocol failure

### Requirement: Read-only and leader-only
The MCP tools SHALL NOT change any state. A non-leader replica SHALL answer `/mcp` with 503.

#### Scenario: Standby replica
- **WHEN** a client calls `/mcp` on a non-leader replica
- **THEN** it gets HTTP 503
