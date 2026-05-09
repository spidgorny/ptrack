# ptrack API contract

This document defines the canonical API contract for `ptrack`.

It is the shared boundary for:

- the Go daemon/server
- the daemon-hosted web UI
- the reusable React component library

All implementations should treat this document and `ptrack-openapi.yaml` as the source of truth for API behavior.

## Scope

`ptrack` only tracks commands started with the `ptrack` prefix.

- `ptrack npm run dev` is tracked
- `npm run dev` is not tracked
- unrelated processes in the same host or container are out of scope

The daemon is long-lived and remains available even after all tracked processes have exited.

## Versioning

- HTTP base path: `/api/v1`
- WebSocket endpoint: `/api/v1/ws`
- Breaking changes require a new versioned path
- Additive fields are allowed within `v1`

## Transport model

### REST

REST is used for:

- daemon status
- initial process list
- process detail lookups
- buffered log retrieval
- child-process snapshots

### WebSocket

WebSocket is used for:

- live process list updates
- live process detail updates
- live log streaming
- live child-process updates

The UI should load initial state over REST and then subscribe over WebSocket for incremental updates.

## Resource model

### Process status

`status` is one of:

- `starting`
- `running`
- `exited`

### Process outcome

`outcome` is one of:

- `unknown`
- `succeeded`
- `failed`
- `signaled`

### CPU metric model

CPU values are normalized to a percentage in the range `0.0` to `100.0 * logical_cpu_count`.

Fields:

- `latest_percent`: most recent sampled CPU usage
- `average_percent`: average CPU usage since process start
- `peak_percent`: highest observed sample
- `sample_count`: number of samples collected
- `last_sample_at`: RFC3339 timestamp of the latest sample

### Log retention model

The daemon keeps a bounded in-memory ring buffer per tracked process.

Fields:

- `retained_bytes`: bytes currently retained
- `truncated`: `true` if older output has been evicted
- `last_seq`: latest emitted log sequence number

## Common schemas

### DaemonInfo

```json
{
  "version": "0.1.0",
  "started_at": "2026-05-09T06:55:00Z",
  "uptime_ms": 285000,
  "socket_path": "/tmp/ptrack.sock",
  "http_address": "127.0.0.1:7777",
  "websocket_path": "/api/v1/ws",
  "tracked_process_count": 2,
  "running_process_count": 1
}
```

### ProcessSummary

```json
{
  "id": "proc_01jtrj4j4x7v9nkp7f2g6h0v9r",
  "command": "npm",
  "argv": ["npm", "run", "dev"],
  "display_name": "npm run dev",
  "cwd": "/workspace/app",
  "status": "running",
  "outcome": "unknown",
  "started_at": "2026-05-09T07:00:01Z",
  "finished_at": null,
  "runtime_ms": 91234,
  "exit_code": null,
  "signal": null,
  "cpu": {
    "latest_percent": 18.5,
    "average_percent": 12.4,
    "peak_percent": 31.2,
    "sample_count": 18,
    "last_sample_at": "2026-05-09T07:01:31Z"
  },
  "logs": {
    "retained_bytes": 48210,
    "truncated": false,
    "last_seq": 184
  },
  "child_process_count": 2
}
```

### ProcessDetail

```json
{
  "id": "proc_01jtrj4j4x7v9nkp7f2g6h0v9r",
  "command": "npm",
  "argv": ["npm", "run", "dev"],
  "display_name": "npm run dev",
  "cwd": "/workspace/app",
  "status": "running",
  "outcome": "unknown",
  "started_at": "2026-05-09T07:00:01Z",
  "finished_at": null,
  "runtime_ms": 91234,
  "exit_code": null,
  "signal": null,
  "pid": 18412,
  "pty": {
    "enabled": true,
    "cols": 120,
    "rows": 30
  },
  "cpu": {
    "latest_percent": 18.5,
    "average_percent": 12.4,
    "peak_percent": 31.2,
    "sample_count": 18,
    "last_sample_at": "2026-05-09T07:01:31Z"
  },
  "logs": {
    "retained_bytes": 48210,
    "truncated": false,
    "last_seq": 184
  },
  "children": [
    {
      "pid": 18421,
      "ppid": 18412,
      "command": "node",
      "argv": ["node", "vite"],
      "status": "running",
      "started_at": "2026-05-09T07:00:03Z",
      "finished_at": null,
      "cpu_percent": 7.1
    }
  ],
  "source": {
    "kind": "ptrack",
    "docker": {
      "in_container": true,
      "container_id": "9f3c2a4f1e7d"
    }
  }
}
```

### LogEntry

```json
{
  "seq": 185,
  "timestamp": "2026-05-09T07:01:32.215Z",
  "stream": "pty",
  "text": "\\u001b[32mready in 320ms\\u001b[0m\\n"
}
```

### ErrorResponse

```json
{
  "error": {
    "code": "process_not_found",
    "message": "No tracked process exists for id proc_missing",
    "details": {
      "id": "proc_missing"
    }
  }
}
```

## HTTP endpoints

### `GET /api/v1/health`

Returns daemon status and version metadata.

### `GET /api/v1/processes`

Query parameters:

- `status`: `all` (default), `running`, `exited`
- `limit`: default `100`, max `500`
- `cursor`: opaque pagination cursor

Returns:

- `items`: array of `ProcessSummary`
- `page.next_cursor`
- daemon counters

### `GET /api/v1/processes/{id}`

Returns `ProcessDetail`.

### `GET /api/v1/processes/{id}/logs`

Query parameters:

- `after`: optional sequence number
- `limit`: default `500`, max `5000`

Returns buffered `LogEntry` items and `page.next_after`.

### `GET /api/v1/processes/{id}/children`

Returns the latest snapshot of child processes within the tracked process tree.

## WebSocket contract

### Endpoint

`GET /api/v1/ws`

### Client messages

Subscribe to all process events:

```json
{
  "type": "subscribe",
  "topic": "processes"
}
```

Subscribe to one process detail stream:

```json
{
  "type": "subscribe",
  "topic": "process",
  "process_id": "proc_01jtrj4j4x7v9nkp7f2g6h0v9r",
  "logs_after": 184
}
```

### Server messages

`hello`, `process.created`, `process.updated`, `process.exited`, `process.log.append`, `process.children.updated`, `error`

Envelope:

```json
{
  "type": "process.updated",
  "timestamp": "2026-05-09T07:01:32.215Z",
  "payload": {}
}
```

## UI integration guidance

### React list view

1. `GET /api/v1/processes?status=all`
2. open `/api/v1/ws`
3. subscribe to `processes`
4. upsert list items on `process.created`, `process.updated`, and `process.exited`

### React detail view

1. `GET /api/v1/processes/{id}`
2. `GET /api/v1/processes/{id}/logs`
3. optionally `GET /api/v1/processes/{id}/children`
4. open `/api/v1/ws`
5. subscribe to `process` with `process_id` and `logs_after`
6. append incoming `process.log.append` messages in `seq` order
