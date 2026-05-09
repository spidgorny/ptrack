import type { CSSProperties } from 'react';
import type { ProcessOutcome, ProcessStatus } from '../types/api';

const palette: Record<ProcessStatus | ProcessOutcome, CSSProperties> = {
  starting: { background: 'rgba(66, 153, 225, 0.18)', color: '#90cdf4' },
  running: { background: 'rgba(72, 187, 120, 0.18)', color: '#9ae6b4' },
  exited: { background: 'rgba(160, 174, 192, 0.2)', color: '#e2e8f0' },
  unknown: { background: 'rgba(160, 174, 192, 0.2)', color: '#d6e3f0' },
  succeeded: { background: 'rgba(72, 187, 120, 0.18)', color: '#9ae6b4' },
  failed: { background: 'rgba(245, 101, 101, 0.18)', color: '#feb2b2' },
  signaled: { background: 'rgba(236, 201, 75, 0.2)', color: '#f6e05e' },
};

export interface StatusBadgeProps {
  label: ProcessStatus | ProcessOutcome;
}

export const StatusBadge = ({ label }: StatusBadgeProps) => (
  <span
    style={{
      ...palette[label],
      display: 'inline-flex',
      alignItems: 'center',
      justifyContent: 'center',
      borderRadius: 999,
      padding: '0.22rem 0.6rem',
      fontSize: '0.78rem',
      fontWeight: 700,
      textTransform: 'capitalize',
      whiteSpace: 'nowrap',
    }}
  >
    {label}
  </span>
);
