import { useCallback, useEffect, useMemo, useState } from 'react';
import { createPtrackApiClient, safeParseServerMessage, type PtrackApiClientOptions } from '../api/client';
import {
  appendLogEntries,
  extractChildrenPayload,
  extractLogPayload,
  extractProcessPayload,
  mergeProcessDetail,
} from '../state/process-events';
import type { ChildProcess, ProcessDetail as ProcessDetailModel, ProcessLogsResponse } from '../types/api';

export interface UseProcessDetailOptions extends PtrackApiClientOptions {
  subscribe?: boolean;
  logLimit?: number;
}

export interface UseProcessDetailResult {
  detail?: ProcessDetailModel;
  logs: ProcessLogsResponse['items'];
  children: ChildProcess[];
  isLoading: boolean;
  error?: Error;
  connectionState: 'idle' | 'connecting' | 'open' | 'closed' | 'error';
  reload: () => void;
}

const asError = (value: unknown) => (value instanceof Error ? value : new Error('Unknown process detail failure'));

export const useProcessDetail = (
  processId: string | undefined,
  options: UseProcessDetailOptions = {},
): UseProcessDetailResult => {
  const client = useMemo(
    () => createPtrackApiClient({ baseUrl: options.baseUrl, wsUrl: options.wsUrl }),
    [options.baseUrl, options.wsUrl],
  );
  const [detail, setDetail] = useState<ProcessDetailModel>();
  const [logs, setLogs] = useState<ProcessLogsResponse['items']>([]);
  const [children, setChildren] = useState<ChildProcess[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error>();
  const [connectionState, setConnectionState] = useState<UseProcessDetailResult['connectionState']>('idle');
  const [reloadKey, setReloadKey] = useState(0);
  const subscribe = options.subscribe ?? true;
  const logLimit = options.logLimit ?? 500;

  useEffect(() => {
    if (!processId) {
      setDetail(undefined);
      setLogs([]);
      setChildren([]);
      setIsLoading(false);
      setError(undefined);
      setConnectionState('idle');
      return;
    }

    let cancelled = false;
    let socket: WebSocket | undefined;

    setIsLoading(true);
    setError(undefined);
    setConnectionState(subscribe ? 'connecting' : 'idle');

    Promise.all([
      client.getProcessDetail(processId),
      client.getProcessLogs(processId, { limit: logLimit }),
      client.getProcessChildren(processId).catch(() => ({ items: [] as ChildProcess[] })),
    ])
      .then(([detailResponse, logResponse, childrenResponse]) => {
        if (cancelled) {
          return;
        }

        const normalizedChildren = Array.isArray(childrenResponse) ? childrenResponse : childrenResponse.items;
        setDetail(detailResponse);
        setLogs(logResponse.items);
        setChildren(normalizedChildren.length > 0 ? normalizedChildren : detailResponse.children);

        if (subscribe) {
          const lastSeq = logResponse.items.at(-1)?.seq ?? detailResponse.logs.last_seq;
          socket = client.createWebSocket({
            type: 'subscribe',
            topic: 'process',
            process_id: processId,
            logs_after: lastSeq,
          });
          socket.addEventListener('open', () => setConnectionState('open'));
          socket.addEventListener('close', () => setConnectionState('closed'));
          socket.addEventListener('error', () => {
            setConnectionState('error');
            setError((current) => current ?? new Error('Process detail socket failed.'));
          });
          socket.addEventListener('message', (event) => {
            const message = typeof event.data === 'string' ? safeParseServerMessage(event.data) : null;
            if (!message || cancelled) {
              return;
            }

            const processPayload = extractProcessPayload(message);
            if (processPayload && processPayload.id === processId) {
              setDetail((current) => mergeProcessDetail(current, processPayload));
            }

            const logPayload = extractLogPayload(message);
            if (logPayload && (!logPayload.processId || logPayload.processId === processId)) {
              setLogs((current) => appendLogEntries(current, [logPayload.entry]));
            }

            const childrenPayload = extractChildrenPayload(message);
            if (childrenPayload && (!childrenPayload.processId || childrenPayload.processId === processId)) {
              setChildren(childrenPayload.children);
            }

            if (message.type === 'error') {
              setError(new Error(message.payload.message));
            }
          });
        }
      })
      .catch((reason) => {
        if (!cancelled) {
          setError(asError(reason));
          setDetail(undefined);
          setLogs([]);
          setChildren([]);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setIsLoading(false);
        }
      });

    return () => {
      cancelled = true;
      socket?.close();
    };
  }, [client, processId, subscribe, logLimit, reloadKey]);

  const reload = useCallback(() => setReloadKey((current) => current + 1), []);

  return { detail, logs, children, isLoading, error, connectionState, reload };
};
