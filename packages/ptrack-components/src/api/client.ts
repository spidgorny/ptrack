import type {
  DaemonInfo,
  ErrorResponse,
  ProcessChildrenResponse,
  ProcessDetail,
  ProcessListParams,
  ProcessListResponse,
  ProcessLogsParams,
  ProcessLogsResponse,
  PtrackClientMessage,
  PtrackServerMessage,
} from '../types/api';

const FALLBACK_BASE_URL = 'http://127.0.0.1:7777';

type FetchLike = typeof fetch;
type WebSocketCtor = typeof WebSocket;

export interface PtrackApiClientOptions {
  baseUrl?: string;
  wsUrl?: string;
  fetchImpl?: FetchLike;
  WebSocketImpl?: WebSocketCtor;
}

export class PtrackApiError extends Error {
  readonly status: number;
  readonly code?: string;
  readonly details?: Record<string, unknown>;

  constructor(message: string, options: { status: number; code?: string; details?: Record<string, unknown> }) {
    super(message);
    this.name = 'PtrackApiError';
    this.status = options.status;
    this.code = options.code;
    this.details = options.details;
  }
}

const isBrowser = () => typeof window !== 'undefined';
const normalizeBaseUrl = (value: string) => value.replace(/\/+$/, '');
const coerceHttpBaseUrl = (value: string) => (/^https?:\/\//.test(value) ? value : `http://${value}`);
const ensureTrailingSlash = (value: string) => (value.endsWith('/') ? value : `${value}/`);

const resolveBrowserBaseUrl = () => normalizeBaseUrl(new URL('.', ensureTrailingSlash(window.location.href)).toString());

export const safeParseServerMessage = (raw: string): PtrackServerMessage | null => {
  try {
    return JSON.parse(raw) as PtrackServerMessage;
  } catch {
    return null;
  }
};

export class PtrackApiClient {
  private readonly options: PtrackApiClientOptions;

  constructor(options: PtrackApiClientOptions = {}) {
    this.options = options;
  }

  resolveBaseUrl(): string {
    if (this.options.baseUrl) {
      return normalizeBaseUrl(coerceHttpBaseUrl(this.options.baseUrl));
    }

    if (isBrowser()) {
      return resolveBrowserBaseUrl();
    }

    return FALLBACK_BASE_URL;
  }

  resolveWebSocketUrl(): string {
    if (this.options.wsUrl) {
      return this.options.wsUrl;
    }

    const baseUrl = new URL(ensureTrailingSlash(this.resolveBaseUrl()));
    baseUrl.protocol = baseUrl.protocol === 'https:' ? 'wss:' : 'ws:';
    baseUrl.pathname = `${baseUrl.pathname.replace(/\/+$/, '/') }api/v1/ws`;
    baseUrl.search = '';
    baseUrl.hash = '';
    return baseUrl.toString();
  }

  private resolveFetch(): FetchLike {
    const fetchImpl = this.options.fetchImpl ?? globalThis.fetch;
    if (!fetchImpl) {
      throw new Error('Fetch is unavailable. Provide fetchImpl in PtrackApiClientOptions.');
    }
    return fetchImpl;
  }

  private resolveWebSocket(): WebSocketCtor {
    const WebSocketImpl = this.options.WebSocketImpl ?? globalThis.WebSocket;
    if (!WebSocketImpl) {
      throw new Error('WebSocket is unavailable. Provide WebSocketImpl in PtrackApiClientOptions.');
    }
    return WebSocketImpl;
  }

  private buildUrl(pathname: string, params?: Record<string, string | number | undefined>) {
    const url = new URL(pathname.replace(/^\/+/, ''), ensureTrailingSlash(this.resolveBaseUrl()));

    Object.entries(params ?? {}).forEach(([key, value]) => {
      if (value !== undefined && value !== '') {
        url.searchParams.set(key, String(value));
      }
    });

    return url.toString();
  }

  private async request<T>(pathname: string, params?: Record<string, string | number | undefined>): Promise<T> {
    const response = await this.resolveFetch()(this.buildUrl(pathname, params), {
      headers: {
        Accept: 'application/json',
      },
    });

    const body = await response.json().catch(() => undefined as unknown);

    if (!response.ok) {
      const errorBody = body as ErrorResponse | undefined;
      throw new PtrackApiError(errorBody?.error.message ?? `Request failed with status ${response.status}`, {
        status: response.status,
        code: errorBody?.error.code,
        details: errorBody?.error.details,
      });
    }

    return body as T;
  }

  async getHealth() {
    return this.request<DaemonInfo>('api/v1/health');
  }

  async listProcesses(params: ProcessListParams = {}) {
    return this.request<ProcessListResponse>('api/v1/processes', {
      status: params.status,
      limit: params.limit,
      cursor: params.cursor,
    });
  }

  async getProcessDetail(id: string) {
    return this.request<ProcessDetail>(`api/v1/processes/${encodeURIComponent(id)}`);
  }

  async getProcessLogs(id: string, params: ProcessLogsParams = {}) {
    return this.request<ProcessLogsResponse>(`api/v1/processes/${encodeURIComponent(id)}/logs`, {
      after: params.after,
      limit: params.limit,
    });
  }

  async getProcessChildren(id: string) {
    return this.request<ProcessChildrenResponse | ProcessChildrenResponse['items']>(
      `api/v1/processes/${encodeURIComponent(id)}/children`,
    );
  }

  createWebSocket(subscription?: PtrackClientMessage) {
    const socket = new (this.resolveWebSocket())(this.resolveWebSocketUrl());

    socket.addEventListener('open', () => {
      if (subscription) {
        socket.send(JSON.stringify(subscription));
      }
    });

    return socket;
  }
}

export const createPtrackApiClient = (options?: PtrackApiClientOptions) => new PtrackApiClient(options);
