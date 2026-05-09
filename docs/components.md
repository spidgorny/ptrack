# ptrack React component docs

This document describes the embeddable React surface exposed by `@ptrack/components`.

It is meant for host applications that want to reuse the same tracked-process experience as the standalone `ptrack` web UI.

## Package purpose

`@ptrack/components` provides:

- typed API models for the `ptrack` daemon
- a small API client for REST and WebSocket access
- hooks for daemon info, process lists, and process details
- UI components for process lists, process details, status badges, and prototype notices

## Main exports

### Hooks

- `useDaemonInfo()` — fetches daemon health/runtime metadata
- `useProcessList(options)` — loads and optionally subscribes to tracked processes
- `useProcessDetail(processId, options)` — loads and optionally subscribes to one process

### Components

- `ProcessList` — renders tracked processes and filter controls
- `ProcessDetail` — renders one tracked process, logs, and child-process details
- `StatusBadge` — small status indicator
- `PrototypeNotice` — informational notice block

## Basic usage

```tsx
import { useState } from 'react';
import {
  ProcessDetail,
  ProcessList,
  useProcessDetail,
  useProcessList,
} from '@ptrack/components';

export function ProcessDashboard() {
  const [selectedProcessId, setSelectedProcessId] = useState<string | undefined>();
  const processList = useProcessList({ subscribe: true, limit: 100 });
  const processDetail = useProcessDetail(selectedProcessId, { subscribe: true, logLimit: 300 });

  return (
    <div style={{ display: 'grid', gridTemplateColumns: '24rem 1fr', gap: '1rem' }}>
      <ProcessList
        items={processList.items}
        selectedId={selectedProcessId}
        onSelect={setSelectedProcessId}
        isLoading={processList.isLoading}
        error={processList.error}
        connectionState={processList.connectionState}
        daemonInfo={processList.daemonInfo}
      />
      <ProcessDetail
        detail={processDetail.detail}
        logs={processDetail.logs}
        children={processDetail.children}
        isLoading={processDetail.isLoading}
        error={processDetail.error}
        connectionState={processDetail.connectionState}
      />
    </div>
  );
}
```

## API targeting

By default the web app proxies requests to the active daemon, but embedded consumers can also pass explicit endpoints:

```tsx
const processList = useProcessList({
  baseUrl: 'http://127.0.0.1:7777',
  wsUrl: 'ws://127.0.0.1:7777/api/v1/ws',
  subscribe: true,
});
```

## Hook options

### `useProcessList(options)`

- `status` — `all`, `running`, or `exited`
- `limit` — maximum number of processes to fetch
- `subscribe` — whether to open a live WebSocket subscription
- `baseUrl` — optional REST base URL override
- `wsUrl` — optional WebSocket URL override

### `useProcessDetail(processId, options)`

- `processId` — tracked process ID
- `logLimit` — number of buffered log entries to fetch initially
- `subscribe` — whether to open a live process-specific WebSocket subscription
- `baseUrl` — optional REST base URL override
- `wsUrl` — optional WebSocket URL override

## Behavior notes

- process list and detail hooks fetch initial REST snapshots first
- when `subscribe` is enabled, they merge live WebSocket updates on top of the snapshot
- stale socket errors are cleared when the socket reconnects successfully
- the current UI surface is read-only; daemon mutation actions are intentionally not exposed yet

## Related docs

- daemon API: `docs/api.md`
- OpenAPI stub: `api/openapi.yaml`
