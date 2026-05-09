import { useCallback, useEffect, useMemo, useState } from 'react';
import { createPtrackApiClient, type PtrackApiClientOptions } from '../api/client';
import type { DaemonInfo } from '../types/api';

export interface UseDaemonInfoResult {
  daemonInfo?: DaemonInfo;
  isLoading: boolean;
  error?: Error;
  reload: () => void;
}

const asError = (value: unknown) => (value instanceof Error ? value : new Error('Unknown daemon request failure'));

export const useDaemonInfo = (options?: PtrackApiClientOptions): UseDaemonInfoResult => {
  const client = useMemo(
    () => createPtrackApiClient({ baseUrl: options?.baseUrl, wsUrl: options?.wsUrl }),
    [options?.baseUrl, options?.wsUrl],
  );
  const [daemonInfo, setDaemonInfo] = useState<DaemonInfo>();
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<Error>();
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);
    setError(undefined);

    client
      .getHealth()
      .then((response) => {
        if (!cancelled) {
          setDaemonInfo(response);
        }
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

    return () => {
      cancelled = true;
    };
  }, [client, reloadKey]);

  const reload = useCallback(() => setReloadKey((current) => current + 1), []);

  return { daemonInfo, isLoading, error, reload };
};
