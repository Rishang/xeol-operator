# Proposal

## Why

Each image is rescanned `interval` (default 24h) after its last scan. After fixing an image, rebuilding it on the same digest's base, or accepting new EOL data, an operator wants fresh results now, for one image or for everything, without restarting the pod (a new pod rescans everything and also re-downloads trivy's DB).

## What Changes

- **CLI:** `/manager rescan [image glob...]`, run inside the pod with `kubectl exec -n xeol-system deploy/xeol-operator -- /manager rescan ...`. No globs = every running image. Globs use the `excludeImages` syntax and match the image as written in the Pod spec or its repository. It prints the queued images.
- The CLI talks to the running operator over a new loopback-only HTTP listener (`--admin-bind-address`, default `127.0.0.1:8082`). Nothing new is reachable from outside the pod; who may rescan is decided by the Kubernetes `pods/exec` permission.
- **MCP:** a `rescan` tool with the same optional `images` globs, only when `--mcp-allow-rescan` (Helm `mcp.allowRescan`, default false) is set. Without it, the MCP server stays read-only as today.
- Rescans go through the existing scan queue: a digest already queued or being scanned is not scanned twice, images in OOM backoff stay skipped, and databases are not refreshed.
- AGENTS.md: the MCP write exception (opt-in rescan) is recorded.

## Non-goals

- Refreshing the EOL or trivy database on demand.
- A rescan button in the web page, or an unauthenticated HTTP rescan endpoint on the metrics port.
- Rescanning images that no running Pod uses (only the inventory's digests can be rescanned).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities
- `image-scanning`: on-demand rescan of all or selected running images.
- `mcp-server`: optional `rescan` tool.
