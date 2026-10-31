# Design

## D1: Queue, not schedule

A rescan request matches inventory images against the globs (the same `excluded` matcher as `excludeImages`: image as written in the Pod spec, or its repository) and calls the scan queue's `Add` for each digest. Workers scan whatever is queued, and the workqueue dedupes, so repeated requests cost at most one extra scan per image. The scanner still applies OOM backoff. The schedule is untouched: the scan sets the next time as usual.

## D2: Loopback admin listener for the CLI

`--admin-bind-address` (default `127.0.0.1:8082`, `0` disables) serves `POST /rescan` with `{"images": [...]}` and answers `{"images": [<refs queued>]}`. It binds to loopback, so only processes in the pod reach it: `kubectl exec ... /manager rescan` runs the CLI in the operator container, guarded by RBAC on `pods/exec`. A standby replica answers 503 (no inventory). Request size is capped (at most 100 globs of 256 characters).

## D3: MCP tool behind a flag

`--mcp-allow-rescan` adds a `rescan` tool (input `images`, optional) to the MCP server, annotated as not read-only, idempotent and not destructive. The MCP endpoint has no auth (same port as the metrics), so it is off by default; the queue dedupe bounds the cost of abuse to one scan per image at a time.

## D4: CLI

`/manager rescan [--addr 127.0.0.1:8082] [glob...]` posts to the admin listener and prints one queued image per line, or "no running image matches". Exit 1 on error. It lives next to `build-db` in `cmd/`.
