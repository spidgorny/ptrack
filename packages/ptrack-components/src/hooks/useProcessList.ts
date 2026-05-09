import { useCallback, useEffect, useMemo, useState } from 'react';
import { createPtrackApiClient, safeParseServerMessage, type PtrackApiClientOptions } from '../api/client';
import { extractProcessPayload, sortProcesses, upsertProcessSummary } from '../state/process-events';
import type { DaemonInfo, ProcessListResponse, ProcessListStatusFilter, ProcessSummary } from '../types/api';

export interface UseProcessListOptions extends PtrackApiClientOptions {
  status?: ProcessListStatusFilter;
  limit?: number;
  subscribe?: boolean;
}

export interface UseProcessListResult {
  items: ProcessSummary[];
  daemonInfo?: Pick<DaemonInfo, 'tracked_process_count' | 'running_process_count'>;
  nextCursor?: string | null;
  isLoading: boolean;
  error?: Error;
  connectionState: 'idle' | 'connecting' | 'open' | 'closed' | 'error';
  reload: () => void;
}

const asError = (value: unknown) => (value instanceof Error ? value : new Error('Unknown process list failure'));
const daemonFromItems = (items: ProcessSummary[]) => ({
  tracked_process_count: items.length,
  running_process_count: items.filter((item) => item.status === 'running').length,
});
const matchesFilter = (item: ProcessSummary, status: ProcessListStatusFilter) => status === 'all' || item.status === status;
const daemonFromResponse = (response: ProcessListResponse) => ({
  tracked_process_count: response.tracked_process_count ?? response.daemon?.tracked_process_count ?? response.items.length,
  running_process_count:
    response.running_process_count ??
    response.daemon?.running_process_count ??
    response.items.filter((item) => item.status === 'running').length,
});

export const useProcessList = (options: UseProcessListOptions = {}): UseProcessListResult => {
  const client = useMemo(
    () => createPtrackApiClient({ baseUrl: options.baseUrl, wsUrl: options.wsUrl }),
    [options.baseUrl, options.wsUrl],
  );
  const [items, setItems] = useState<ProcessSummary[]>([]);
  const [daemonInfo, setDaemonInfo] = useState<UseProcessListResult['daemonInfo']>();
  const [nextCursor, setNextCursor] = useState<string | null>();
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<Error>();
  const [connectionState, setConnectionState] = useState<UseProcessListResult['connectionState']>('idle');
  const [reloadKey, setReloadKey] = useState(0);
  const status = options.status ?? 'all';
  const limit = options.limit ?? 100;
  const subscribe = options.subscribe ?? true;

  useEffect(() => {
    let cancelled = false;
    let socket: WebSocket | undefined;

    setIsLoading(true);
    setError(undefined);
    setConnectionState(subscribe ? 'connecting' : 'idle');

    client
      .listProcesses({ status, limit })
      .then((response) => {
        if (cancelled) {
          return;
        }
        const sortedItems = sortProcesses(response.items);
        setItems(sortedItems);
        setDaemonInfo(daemonFromResponse(response));
        setNextCursor(response.page?.next_cursor ?? null);
      })
      .catch((reason) => {
        if (!cancelled) {
          setError(asError(reason));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setIsLoading(false);
        }
      });

    if (subscribe) {
      socket = client.createWebSocket({ type: 'subscribe', topic: 'processes' });
      socket.addEventListener('open', () => {
        setConnectionState('open');
        setError((current) => (current?.message === 'Process list socket failed.' ? undefined : current));
      });
      socket.addEventListener('close', () => setConnectionState('closed'));
      socket.addEventListener('error', () => {
        setConnectionState('error');
        setError((current) => current ?? new Error('Process list socket failed.'));
      });
      socket.addEventListener('message', (event) => {
        const message = typeof event.data === 'string' ? safeParseServerMessage(event.data) : null;
        if (!message || cancelled) {
          return;
        }

        const payload = extractProcessPayload(message);
        if (payload) {
          setItems((current) => {
            const nextItems = matchesFilter(payload, status)
              ? upsertProcessSummary(current, payload)
              : sortProcesses(current.filter((item) => item.id !== payload.id));
            setDaemonInfo(daemonFromItems(nextItems));
            return nextItems;
          });
        }

        if (message.type === 'error') {
          setError(new Error(message.payload.message));
        }
      });
    }

    return () => {
      cancelled = true;
      socket?.close();
    };
  }, [client, status, limit, subscribe, reloadKey]);

  const reload = useCallback(() => setReloadKey((current) => current + 1), []);

  return { items, daemonInfo, nextCursor, isLoading, error, connectionState, reload };
};
