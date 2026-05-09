import { useEffect, useMemo, useState } from 'react';
import {
  ProcessDetail,
  ProcessList,
  PrototypeNotice,
  useDaemonInfo,
  useProcessDetail,
  useProcessList,
  type ProcessListStatusFilter,
} from '@ptrack/components';

const appHeaderStats = (label: string, value: string | number | undefined) => (
  <div className="app-stat-card" key={label}>
    <span className="app-stat-label">{label}</span>
    <strong className="app-stat-value">{value ?? '—'}</strong>
  </div>
);

export function App() {
  const [statusFilter, setStatusFilter] = useState<ProcessListStatusFilter>('all');
  const [selectedProcessId, setSelectedProcessId] = useState<string | undefined>();

  const daemon = useDaemonInfo();
  const processList = useProcessList({ status: statusFilter, limit: 100, subscribe: true });
  const processDetail = useProcessDetail(selectedProcessId, { subscribe: true, logLimit: 300 });

  useEffect(() => {
    if (selectedProcessId && processList.items.some((item) => item.id === selectedProcessId)) {
      return;
    }

    setSelectedProcessId(processList.items[0]?.id);
  }, [processList.items, selectedProcessId]);

  const headerStats = useMemo(
    () => [
      appHeaderStats('Tracked', daemon.daemonInfo?.tracked_process_count),
      appHeaderStats('Running', daemon.daemonInfo?.running_process_count),
      appHeaderStats('Socket', daemon.daemonInfo?.socket_path),
      appHeaderStats('HTTP', daemon.daemonInfo?.http_address),
    ],
    [daemon.daemonInfo],
  );

  return (
    <div className="app-shell">
      <header className="app-header">
        <div>
          <p className="app-eyebrow">ptrack prototype</p>
          <h1>Tracked process dashboard</h1>
          <p className="app-subtitle">
            Daemon-hosted web UI prototype wired to the v1 REST and WebSocket contract.
          </p>
        </div>
        <div className="app-stat-grid">{headerStats}</div>
      </header>

      <PrototypeNotice
        title="Prototype safeguards"
        message="Mutation endpoints are not defined in the current API contract, so launch, restart, and terminate controls remain intentionally disabled."
      />

      <main className="app-main">
        <section className="app-panel app-panel--list" aria-label="Process list">
          <ProcessList
            items={processList.items}
            selectedId={selectedProcessId}
            onSelect={setSelectedProcessId}
            statusFilter={statusFilter}
            onStatusFilterChange={setStatusFilter}
            isLoading={processList.isLoading}
            error={processList.error}
            connectionState={processList.connectionState}
            daemonInfo={daemon.daemonInfo}
          />
        </section>
        <section className="app-panel app-panel--detail" aria-label="Process detail">
          <ProcessDetail
            detail={processDetail.detail}
            logs={processDetail.logs}
            children={processDetail.children}
            isLoading={processDetail.isLoading}
            error={processDetail.error}
            connectionState={processDetail.connectionState}
            onRetry={selectedProcessId ? processDetail.reload : undefined}
          />
        </section>
      </main>
    </div>
  );
}
