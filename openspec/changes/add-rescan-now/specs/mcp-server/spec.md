## ADDED Requirements

### Requirement: Optional rescan tool
With `--mcp-allow-rescan` (Helm `mcp.allowRescan`), the MCP server SHALL add a `rescan` tool that queues an on-demand rescan of all running images or of those matching its optional `images` globs, and returns the queued images. Without the flag the tool SHALL not exist and the server stays read-only.

#### Scenario: Flag off
- **WHEN** the operator runs with `--mcp` and without `--mcp-allow-rescan`
- **THEN** `tools/list` has no `rescan` tool

#### Scenario: Rescan through MCP
- **WHEN** `--mcp-allow-rescan` is set and a client calls `rescan` with `images: ["registry.internal/*"]`
- **THEN** the matching running images are queued and listed in the result
