import type { CSSProperties } from 'react';
import type { DaemonInfo, ProcessListStatusFilter, ProcessSummary } from '../types/api';
import { StatusBadge } from './StatusBadge';

const containerStyle: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  height: '100%',
  minHeight: 0,
  border: '1px solid rgba(159, 180, 208, 0.15)',
  borderRadius: 20,
  background: 'rgba(7, 14, 27, 0.78)',
  backdropFilter: 'blur(18px)',
  overflow: 'hidden',
};

const headerStyle: CSSProperties = {
  padding: '1rem 1rem 0.85rem',
  borderBottom: '1px solid rgba(159, 180, 208, 0.12)',
};

const listStyle: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: '0.75rem',
  padding: '1rem',
  overflow: 'auto',
};

const statBlockStyle: CSSProperties = {
  padding: '0.75rem',
  borderRadius: 14,
  background: 'rgba(15, 23, 42, 0.75)',
  border: '1px solid rgba(159, 180, 208, 0.12)',
};

const formatDuration = (runtimeMs: number) => {
  const seconds = Math.round(runtimeMs / 1000);
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  if (minutes < 1) {
    return `${seconds}s`;
  }
  return `${minutes}m ${remainingSeconds}s`;
};

const formatCpu = (value: number) => `${value.toFixed(1)}%`;

export interface ProcessListProps {
  items: ProcessSummary[];
  selectedId?: string;
  statusFilter: ProcessListStatusFilter;
  onStatusFilterChange: (value: ProcessListStatusFilter) => void;
  onSelect: (processId: string) => void;
  isLoading?: boolean;
  error?: Error;
  connectionState?: 'idle' | 'connecting' | 'open' | 'closed' | 'error';
  daemonInfo?: DaemonInfo | Pick<DaemonInfo, 'tracked_process_count' | 'running_process_count'>;
}

export const ProcessList = ({
  items,
  selectedId,
  statusFilter,
  onStatusFilterChange,
  onSelect,
  isLoading,
  error,
  connectionState,
  daemonInfo,
}: ProcessListProps) => (
  <div style={containerStyle}>
    <div style={headerStyle}>
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: '0.75rem', alignItems: 'center' }}>
        <div>
          <h2 style={{ margin: 0, fontSize: '1.15rem' }}>Processes</h2>
          <p style={{ margin: '0.35rem 0 0', color: '#9fb4d0' }}>
            Initial state loads over REST and stays fresh through WebSocket updates.
          </p>
        </div>
        <label style={{ display: 'grid', gap: '0.35rem', color: '#9fb4d0' }}>
          <span style={{ fontSize: '0.8rem', textTransform: 'uppercase', letterSpacing: '0.08em' }}>Filter</span>
          <select
            value={statusFilter}
            onChange={(event) => onStatusFilterChange(event.target.value as ProcessListStatusFilter)}
            style={{
              background: 'rgba(15, 23, 42, 0.85)',
              color: '#ffffff',
              border: '1px solid rgba(159, 180, 208, 0.2)',
              borderRadius: 10,
              padding: '0.5rem 0.7rem',
            }}
          >
            <option value="all">All</option>
            <option value="running">Running</option>
            <option value="exited">Exited</option>
          </select>
        </label>
      </div>

      <div style={{ display: 'grid', gap: '0.75rem', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', marginTop: '1rem' }}>
        <div style={statBlockStyle}>
          <div style={{ color: '#9fb4d0', fontSize: '0.8rem', marginBottom: '0.25rem' }}>Tracked</div>
          <strong style={{ fontSize: '1.3rem' }}>{daemonInfo?.tracked_process_count ?? items.length}</strong>
        </div>
        <div style={statBlockStyle}>
          <div style={{ color: '#9fb4d0', fontSize: '0.8rem', marginBottom: '0.25rem' }}>Running</div>
          <strong style={{ fontSize: '1.3rem' }}>
            {daemonInfo?.running_process_count ?? items.filter((item) => item.status === 'running').length}
          </strong>
        </div>
      </div>

      <p style={{ margin: '0.85rem 0 0', color: '#9fb4d0', fontSize: '0.9rem' }}>
        Connection: <strong style={{ color: '#ffffff', textTransform: 'capitalize' }}>{connectionState ?? 'idle'}</strong>
      </p>
      {error ? <p style={{ margin: '0.75rem 0 0', color: '#feb2b2' }}>{error.message}</p> : null}
    </div>

    <div style={listStyle}>
      {isLoading && items.length === 0 ? <div style={statBlockStyle}>Loading process list…</div> : null}

      {!isLoading && items.length === 0 ? (
        <div style={statBlockStyle}>
          No tracked processes are available for the selected filter. This empty state is safe for prototype use.
        </div>
      ) : null}

      {items.map((process) => {
        const isSelected = process.id === selectedId;
        return (
          <button
            key={process.id}
            type="button"
            onClick={() => onSelect(process.id)}
            style={{
              textAlign: 'left',
              width: '100%',
              border: `1px solid ${isSelected ? 'rgba(99, 179, 237, 0.62)' : 'rgba(159, 180, 208, 0.15)'}`,
              borderRadius: 18,
              background: isSelected ? 'rgba(37, 99, 235, 0.2)' : 'rgba(10, 16, 32, 0.88)',
              color: '#ffffff',
              padding: '0.9rem',
              cursor: 'pointer',
            }}
          >
            <div style={{ display: 'flex', justifyContent: 'space-between', gap: '0.75rem', alignItems: 'flex-start' }}>
              <div>
                <strong style={{ display: 'block', fontSize: '0.98rem' }}>{process.display_name}</strong>
                <span style={{ color: '#9fb4d0', fontSize: '0.85rem' }}>{process.cwd}</span>
              </div>
              <StatusBadge label={process.status} />
            </div>

            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.55rem', marginTop: '0.8rem' }}>
              <span style={{ color: '#bfd0e5' }}>{formatDuration(process.runtime_ms)}</span>
              <span style={{ color: '#bfd0e5' }}>CPU {formatCpu(process.cpu.latest_percent)}</span>
              <span style={{ color: '#bfd0e5' }}>{process.logs.retained_bytes.toLocaleString()} B logs</span>
              <span style={{ color: '#bfd0e5' }}>{process.child_process_count} children</span>
            </div>

            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.55rem', marginTop: '0.8rem', alignItems: 'center' }}>
              <StatusBadge label={process.outcome} />
              <code style={{ color: '#9fb4d0', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {process.argv.join(' ')}
              </code>
            </div>
          </button>
        );
      })}
    </div>
  </div>
);
