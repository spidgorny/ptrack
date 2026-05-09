export type ProcessStatus = 'starting' | 'running' | 'exited';
export type ProcessOutcome = 'unknown' | 'succeeded' | 'failed' | 'signaled';
export type ProcessListStatusFilter = 'all' | 'running' | 'exited';
export type LogStream = 'stdout' | 'stderr' | 'pty';

export interface CpuMetrics {
  latest_percent: number;
  average_percent: number;
  peak_percent: number;
  sample_count: number;
  last_sample_at: string | null;
}

export interface LogRetentionInfo {
  retained_bytes: number;
  truncated: boolean;
  last_seq: number;
}

export interface DaemonInfo {
  version: string;
  started_at: string;
  uptime_ms: number;
  socket_path: string;
  http_address: string;
  websocket_path: string;
  tracked_process_count: number;
  running_process_count: number;
}

export interface ProcessSummary {
  id: string;
  command: string;
  argv: string[];
  display_name: string;
  cwd: string;
  status: ProcessStatus;
  outcome: ProcessOutcome;
  started_at: string;
  finished_at: string | null;
  runtime_ms: number;
  exit_code: number | null;
  signal: string | null;
  cpu: CpuMetrics;
  logs: LogRetentionInfo;
  child_process_count: number;
}

export interface PtyInfo {
  enabled: boolean;
  cols: number;
  rows: number;
}

export interface ChildProcess {
  pid: number;
  ppid: number;
  command: string;
  argv: string[];
  status: ProcessStatus;
  started_at: string;
  finished_at: string | null;
  cpu_percent: number;
}

export interface ProcessSource {
  kind: 'ptrack' | string;
  docker?: {
    in_container: boolean;
    container_id: string | null;
  };
}

export interface ProcessDetail extends ProcessSummary {
  pid: number;
  pty: PtyInfo;
  children: ChildProcess[];
  source: ProcessSource;
}

export interface LogEntry {
  seq: number;
  timestamp: string;
  stream: LogStream;
  text: string;
}

export interface ErrorResponse {
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
  };
}

export interface ProcessListResponse {
  items: ProcessSummary[];
  page?: {
    next_cursor?: string | null;
  };
  tracked_process_count?: number;
  running_process_count?: number;
  daemon?: Pick<DaemonInfo, 'tracked_process_count' | 'running_process_count'>;
}

export interface ProcessLogsResponse {
  items: LogEntry[];
  page?: {
    next_after?: number | null;
  };
}

export interface ProcessChildrenResponse {
  items: ChildProcess[];
}

export interface ProcessListParams {
  status?: ProcessListStatusFilter;
  limit?: number;
  cursor?: string;
}

export interface ProcessLogsParams {
  after?: number;
  limit?: number;
}

export interface PtrackSubscribeProcessesMessage {
  type: 'subscribe';
  topic: 'processes';
}

export interface PtrackSubscribeProcessMessage {
  type: 'subscribe';
  topic: 'process';
  process_id: string;
  logs_after?: number;
}

export type PtrackClientMessage =
  | PtrackSubscribeProcessesMessage
  | PtrackSubscribeProcessMessage;

export interface PtrackServerEnvelope<TType extends string, TPayload> {
  type: TType;
  timestamp: string;
  payload: TPayload;
}

export type PtrackServerMessage =
  | PtrackServerEnvelope<'hello', { daemon: DaemonInfo }>
  | PtrackServerEnvelope<'process.created', ProcessSummary | { process: ProcessSummary }>
  | PtrackServerEnvelope<'process.updated', ProcessSummary | ProcessDetail | { process: ProcessSummary | ProcessDetail }>
  | PtrackServerEnvelope<'process.exited', ProcessSummary | ProcessDetail | { process: ProcessSummary | ProcessDetail }>
  | PtrackServerEnvelope<'process.log.append', LogEntry | { process_id?: string; entry: LogEntry }>
  | PtrackServerEnvelope<'process.children.updated', ChildProcess[] | { process_id?: string; children: ChildProcess[] }>
  | PtrackServerEnvelope<'error', ErrorResponse['error']>;
