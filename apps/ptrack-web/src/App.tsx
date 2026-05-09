import { useEffect, useMemo, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import {
  ProcessDetail,
  ProcessList,
  PrototypeNotice,
  useDaemonInfo,
  useProcessDetail,
  useProcessList,
  type ProcessListStatusFilter,
} from '@ptrack/components';
import apiDocs from '../../../docs/api.md?raw';
import componentDocs from '../../../docs/components.md?raw';

type AppPage = 'dashboard' | 'docs-api' | 'docs-components';

const readPageFromHash = (): AppPage => {
  switch (window.location.hash.replace(/^#/, '')) {
    case 'docs-api':
      return 'docs-api';
    case 'docs-components':
      return 'docs-components';
    default:
      return 'dashboard';
  }
};

const appHeaderStats = (label: string, value: string | number | undefined) => (
  <div className="app-stat-card" key={label}>
    <span className="app-stat-label">{label}</span>
    <strong className="app-stat-value">{value ?? '—'}</strong>
  </div>
);

function AppNav({ page }: { page: AppPage }) {
  const items: Array<{ id: AppPage; label: string }> = [
    { id: 'dashboard', label: 'Dashboard' },
    { id: 'docs-api', label: 'API docs' },
    { id: 'docs-components', label: 'Component docs' },
  ];

  return (
    <nav className="app-nav" aria-label="Primary">
      {items.map((item) => (
        <a key={item.id} href={item.id === 'dashboard' ? '#' : `#${item.id}`} className={page === item.id ? 'app-nav-link active' : 'app-nav-link'}>
          {item.label}
        </a>
      ))}
    </nav>
  );
}

function DashboardPage() {
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
          <p className="app-eyebrow">ptrack</p>
          <h1>Tracked process dashboard</h1>
          <p className="app-subtitle">
            Daemon-hosted web UI wired to the live REST and WebSocket contract.
          </p>
        </div>
        <div className="app-stat-grid">{headerStats}</div>
      </header>

      <PrototypeNotice
        title="Process visibility"
        message="Use the dashboard for live tracked processes, then switch to the docs pages to inspect the API contract and embeddable React component surface from the browser."
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

function DocsPage({ title, subtitle, markdown, source }: { title: string; subtitle: string; markdown: string; source: string }) {
  return (
    <div className="app-shell">
      <header className="app-header app-header--docs">
        <div>
          <p className="app-eyebrow">ptrack docs</p>
          <h1>{title}</h1>
          <p className="app-subtitle">{subtitle}</p>
        </div>
        <div className="app-stat-grid">
          {appHeaderStats('Source', source)}
          {appHeaderStats('Format', 'Markdown')}
        </div>
      </header>

      <PrototypeNotice
        title="Docs in the browser"
        message="These pages render repository markdown directly in the UI so you can inspect the daemon API and the React component surface without leaving the running app."
      />

      <main className="docs-main">
        <article className="docs-markdown">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{markdown}</ReactMarkdown>
        </article>
      </main>
    </div>
  );
}

export function App() {
  const [page, setPage] = useState<AppPage>(readPageFromHash);

  useEffect(() => {
    const onHashChange = () => setPage(readPageFromHash());
    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
  }, []);

  return (
    <>
      <AppNav page={page} />
      {page === 'dashboard' ? <DashboardPage /> : null}
      {page === 'docs-api' ? (
        <DocsPage
          title="API documentation"
          subtitle="The canonical REST and WebSocket contract used by the Go daemon, the standalone web UI, and the reusable React component library."
          markdown={apiDocs}
          source="docs/api.md"
        />
      ) : null}
      {page === 'docs-components' ? (
        <DocsPage
          title="React component documentation"
          subtitle="Usage notes for the embeddable hooks and components exposed by @ptrack/components."
          markdown={componentDocs}
          source="docs/components.md"
        />
      ) : null}
    </>
  );
}
