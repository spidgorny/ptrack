import { useEffect, useRef, type CSSProperties } from 'react';
import type { ChildProcess, LogEntry, ProcessDetail as ProcessDetailModel } from '../types/api';
import { PrototypeNotice } from './PrototypeNotice';
import { StatusBadge } from './StatusBadge';

const shellStyle: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: '1rem',
  minHeight: 0,
  height: '100%',
  border: '1px solid rgba(159, 180, 208, 0.15)',
  borderRadius: 20,
  background: 'rgba(7, 14, 27, 0.78)',
  backdropFilter: 'blur(18px)',
  padding: '1rem',
};

const panelStyle: CSSProperties = {
  border: '1px solid rgba(159, 180, 208, 0.12)',
  borderRadius: 18,
  background: 'rgba(10, 16, 32, 0.84)',
  padding: '1rem',
};

const preStyle: CSSProperties = {
  margin: 0,
  overflow: 'auto',
  padding: '1rem',
  borderRadius: 16,
  background: '#020817',
  color: '#d6e3f0',
  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  fontSize: '0.85rem',
  whiteSpace: 'break-spaces',
  minHeight: 0,
};

const terminalStyle: CSSProperties = {
  ...preStyle,
  display: 'flex',
  flexDirection: 'column',
  gap: '0.45rem',
  background: '#000000',
  color: '#f8fafc',
  lineHeight: 1.45,
  flex: 1,
};

const labelStyle: CSSProperties = {
  color: '#9fb4d0',
  fontSize: '0.8rem',
  textTransform: 'uppercase',
  letterSpacing: '0.08em',
};

const formatDate = (value: string | null) => (value ? new Date(value).toLocaleString() : '—');
const formatDuration = (runtimeMs: number) => `${Math.round(runtimeMs / 1000)}s`;
const formatCommand = (argv: string[]) => (argv.length > 0 ? argv.join(' ') : '—');

const EmptyState = () => (
  <div style={{ ...shellStyle, justifyContent: 'center', alignItems: 'center', textAlign: 'center' }}>
    <h2 style={{ margin: 0 }}>Select a tracked process</h2>
    <p style={{ margin: '0.5rem 0 0', maxWidth: '26rem', color: '#9fb4d0' }}>
      The detail pane will request REST snapshots and then subscribe to the per-process WebSocket topic.
    </p>
  </div>
);

const logStreamTone = (stream: LogEntry['stream']): CSSProperties => {
  switch (stream) {
    case 'stderr':
      return {
        color: '#fecaca',
        borderLeft: '3px solid rgba(248, 113, 113, 0.65)',
        background: 'rgba(127, 29, 29, 0.18)',
      };
    case 'stdout':
      return {
        color: '#d6e3f0',
        borderLeft: '3px solid rgba(96, 165, 250, 0.55)',
        background: 'rgba(30, 64, 175, 0.12)',
      };
    case 'pty':
      return {
        color: '#bfdbfe',
        borderLeft: '3px solid rgba(34, 197, 94, 0.6)',
        background: 'rgba(20, 83, 45, 0.18)',
      };
    default:
      return { color: '#f8fafc' };
  }
};

const renderLogEntry = (entry: LogEntry) => (
  <div
    key={entry.seq}
    style={{
      ...logStreamTone(entry.stream),
      padding: '0.45rem 0.7rem',
      borderRadius: 12,
    }}
  >
    {entry.text}
  </div>
);

const sourceSummary = (detail: ProcessDetailModel) => {
  const containerId = detail.source.docker?.container_id;
  if (!detail.source.docker?.in_container) {
    return detail.source.kind;
  }
  return `${detail.source.kind} · container ${containerId ?? 'unknown'}`;
};

export interface ProcessDetailProps {
  detail?: ProcessDetailModel;
  logs?: LogEntry[];
  children?: ChildProcess[];
  isLoading?: boolean;
  error?: Error;
  connectionState?: 'idle' | 'connecting' | 'open' | 'closed' | 'error';
  onRetry?: () => void;
}

export const ProcessDetail = ({
  detail,
  logs = [],
  children = [],
  isLoading,
  error,
  connectionState,
  onRetry,
}: ProcessDetailProps) => {
  const logViewportRef = useRef<HTMLDivElement>(null);
  const stickToBottomRef = useRef(true);

  useEffect(() => {
    const viewport = logViewportRef.current;
    if (!viewport || !stickToBottomRef.current) {
      return;
    }

    viewport.scrollTop = viewport.scrollHeight;
  }, [logs]);

  if (!detail && !isLoading && !error) {
    return <EmptyState />;
  }

  return (
    <div style={shellStyle}>
      {detail ? (
        <>
          <header style={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', gap: '1rem' }}>
            <div>
              <p style={{ ...labelStyle, margin: 0 }}>Process detail</p>
              <h2 style={{ margin: '0.2rem 0 0' }}>{detail.display_name}</h2>
              <p style={{ margin: '0.4rem 0 0', color: '#9fb4d0' }}>{detail.cwd}</p>
            </div>
            <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap', alignItems: 'center' }}>
              <StatusBadge label={detail.status} />
              <StatusBadge label={detail.outcome} />
              <button
                type="button"
                disabled
                title="Prototype placeholder pending mutation endpoints"
                style={{
                  borderRadius: 999,
                  border: '1px dashed rgba(159, 180, 208, 0.35)',
                  background: 'transparent',
                  color: '#9fb4d0',
                  padding: '0.45rem 0.8rem',
                }}
              >
                Terminate (placeholder)
              </button>
            </div>
          </header>

          <PrototypeNotice
            title="Read-only prototype"
            message="Controls that would mutate daemon state stay disabled until the API defines explicit endpoints and authorization semantics."
          />

          <div style={{ display: 'grid', gap: '1rem', gridTemplateColumns: 'repeat(auto-fit, minmax(13rem, 1fr))' }}>
            {[
              ['PID', detail.pid],
              ['Runtime', formatDuration(detail.runtime_ms)],
              ['Started', formatDate(detail.started_at)],
              ['Finished', formatDate(detail.finished_at)],
              ['PTY', detail.pty.enabled ? `${detail.pty.cols}×${detail.pty.rows}` : 'Disabled'],
              ['Source', sourceSummary(detail)],
              ['CPU latest', `${detail.cpu.latest_percent.toFixed(1)}%`],
              ['CPU avg', `${detail.cpu.average_percent.toFixed(1)}%`],
              ['CPU peak', `${detail.cpu.peak_percent.toFixed(1)}%`],
              ['Logs retained', `${detail.logs.retained_bytes.toLocaleString()} bytes`],
              ['Log truncated', detail.logs.truncated ? 'Yes' : 'No'],
              ['Socket state', connectionState ?? 'idle'],
            ].map(([label, value]) => (
              <div key={String(label)} style={panelStyle}>
                <div style={labelStyle}>{label}</div>
                <div style={{ marginTop: '0.45rem', fontWeight: 700 }}>{String(value)}</div>
              </div>
            ))}
          </div>

          <section style={panelStyle}>
            <div style={labelStyle}>Command</div>
            <pre style={{ ...preStyle, marginTop: '0.75rem', maxHeight: '10rem' }}>{formatCommand(detail.argv)}</pre>
          </section>

          <section style={{ ...panelStyle, minHeight: 0, display: 'flex', flexDirection: 'column', flex: 1, overflow: 'hidden' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', gap: '1rem', alignItems: 'center' }}>
              <div>
                <div style={labelStyle}>Buffered logs</div>
                <p style={{ margin: '0.35rem 0 0', color: '#9fb4d0' }}>
                  Showing the latest {logs.length} retained entries. WebSocket append events are merged by sequence number.
                </p>
              </div>
              {onRetry ? (
                <button
                  type="button"
                  onClick={onRetry}
                  style={{
                    borderRadius: 999,
                    border: '1px solid rgba(99, 179, 237, 0.35)',
                    background: 'rgba(37, 99, 235, 0.2)',
                    color: '#ffffff',
                    padding: '0.45rem 0.8rem',
                    cursor: 'pointer',
                  }}
                >
                  Refresh snapshot
                </button>
              ) : null}
            </div>
            <div
              ref={logViewportRef}
              role="log"
              aria-live="polite"
              onScroll={(event) => {
                const viewport = event.currentTarget;
                const distanceFromBottom = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight;
                stickToBottomRef.current = distanceFromBottom < 24;
              }}
              style={{ ...terminalStyle, marginTop: '0.9rem' }}
            >
              {logs.length > 0 ? logs.map(renderLogEntry) : 'No log data has been buffered yet.'}
            </div>
          </section>

          <section style={panelStyle}>
            <div>
              <div style={labelStyle}>Child processes</div>
              <p style={{ margin: '0.35rem 0 0', color: '#9fb4d0' }}>
                Latest snapshot from <code>/api/v1/processes/{detail.id}/children</code> and live updates.
              </p>
            </div>
            {children.length === 0 ? (
              <p style={{ margin: '0.9rem 0 0', color: '#9fb4d0' }}>No child process data is currently retained.</p>
            ) : (
              <div style={{ overflow: 'auto', marginTop: '0.9rem' }}>
                <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                  <thead>
                    <tr>
                      {['PID', 'Command', 'Args', 'Status', 'CPU'].map((header) => (
                        <th
                          key={header}
                          style={{
                            textAlign: 'left',
                            paddingBottom: '0.55rem',
                            borderBottom: '1px solid rgba(159, 180, 208, 0.12)',
                            color: '#9fb4d0',
                            fontSize: '0.8rem',
                          }}
                        >
                          {header}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {children.map((child) => (
                      <tr key={`${child.pid}:${child.started_at}`}>
                        <td style={{ paddingTop: '0.7rem' }}>{child.pid}</td>
                        <td style={{ paddingTop: '0.7rem' }}>{child.command}</td>
                        <td style={{ paddingTop: '0.7rem' }}>{child.argv.join(' ')}</td>
                        <td style={{ paddingTop: '0.7rem', textTransform: 'capitalize' }}>{child.status}</td>
                        <td style={{ paddingTop: '0.7rem' }}>{child.cpu_percent.toFixed(1)}%</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      ) : null}

      {isLoading ? <div style={panelStyle}>Loading process detail…</div> : null}
      {error ? <div style={{ ...panelStyle, color: '#feb2b2' }}>{error.message}</div> : null}
    </div>
  );
};
