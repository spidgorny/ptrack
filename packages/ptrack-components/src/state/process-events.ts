import type { ChildProcess, LogEntry, ProcessDetail, ProcessSummary, PtrackServerMessage } from '../types/api';

const startedAtValue = (value: string) => Date.parse(value || '') || 0;

export const sortProcesses = (items: ProcessSummary[]) =>
  [...items].sort((left, right) => {
    const statusRank = (item: ProcessSummary) => (item.status === 'running' ? 0 : item.status === 'starting' ? 1 : 2);
    return statusRank(left) - statusRank(right) || startedAtValue(right.started_at) - startedAtValue(left.started_at);
  });

export const upsertProcessSummary = (items: ProcessSummary[], incoming: ProcessSummary) => {
  const nextItems = items.filter((item) => item.id !== incoming.id);
  nextItems.push(incoming);
  return sortProcesses(nextItems);
};

export const mergeProcessDetail = (
  current: ProcessDetail | undefined,
  incoming: ProcessSummary | ProcessDetail,
): ProcessDetail | undefined => {
  if (!current && !('pid' in incoming)) {
    return undefined;
  }

  if (!current && 'pid' in incoming) {
    return incoming;
  }

  return {
    ...(current as ProcessDetail),
    ...incoming,
    pty: 'pty' in incoming ? incoming.pty : (current as ProcessDetail).pty,
    children: 'children' in incoming ? incoming.children : (current as ProcessDetail).children,
    source: 'source' in incoming ? incoming.source : (current as ProcessDetail).source,
  };
};

export const appendLogEntries = (existing: LogEntry[], nextEntries: LogEntry[]) => {
  const bySequence = new Map<number, LogEntry>();
  [...existing, ...nextEntries].forEach((entry) => {
    bySequence.set(entry.seq, entry);
  });

  return [...bySequence.values()].sort((left, right) => left.seq - right.seq);
};

export const extractProcessPayload = (message: PtrackServerMessage): ProcessSummary | ProcessDetail | null => {
  if (!message.type.startsWith('process.') || message.type === 'process.log.append' || message.type === 'process.children.updated') {
    return null;
  }

  const payload = message.payload as ProcessSummary | ProcessDetail | { process?: ProcessSummary | ProcessDetail };
  if (payload && typeof payload === 'object' && 'id' in payload) {
    return payload;
  }
  if (payload && typeof payload === 'object' && 'process' in payload && payload.process) {
    return payload.process;
  }
  return null;
};

export const extractLogPayload = (message: PtrackServerMessage): { processId?: string; entry: LogEntry } | null => {
  if (message.type !== 'process.log.append') {
    return null;
  }

  const payload = message.payload as LogEntry | { process_id?: string; entry: LogEntry };
  if (payload && typeof payload === 'object' && 'entry' in payload) {
    return {
      processId: payload.process_id,
      entry: payload.entry,
    };
  }
  if (payload && typeof payload === 'object' && 'seq' in payload) {
    return { entry: payload };
  }
  return null;
};

export const extractChildrenPayload = (message: PtrackServerMessage): { processId?: string; children: ChildProcess[] } | null => {
  if (message.type !== 'process.children.updated') {
    return null;
  }

  const payload = message.payload as ChildProcess[] | { process_id?: string; children: ChildProcess[] };
  if (Array.isArray(payload)) {
    return { children: payload };
  }
  if (payload && typeof payload === 'object' && 'children' in payload) {
    return {
      processId: payload.process_id,
      children: payload.children,
    };
  }
  return null;
};
