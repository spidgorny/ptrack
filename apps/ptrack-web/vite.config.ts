import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

interface DaemonState {
  http_address?: string;
}

const configDir = dirname(fileURLToPath(import.meta.url));
const workspaceRoot = resolve(configDir, '../..');
const daemonStateFile = resolve(workspaceRoot, '.ptrack/daemon.json');
const defaultDaemonTarget = 'http://127.0.0.1:7777';

const normalizeTarget = (value: string) => (/^https?:\/\//.test(value) ? value : `http://${value}`);

const readDaemonTarget = () => {
  if (!existsSync(daemonStateFile)) {
    return undefined;
  }

  try {
    const state = JSON.parse(readFileSync(daemonStateFile, 'utf8')) as DaemonState;
    if (!state.http_address) {
      return undefined;
    }
    return normalizeTarget(state.http_address);
  } catch {
    return undefined;
  }
};

const resolveDaemonTarget = () => {
  if (process.env.PTRACK_HTTP_ADDRESS) {
    return normalizeTarget(process.env.PTRACK_HTTP_ADDRESS);
  }

  return readDaemonTarget() ?? defaultDaemonTarget;
};

export default defineConfig(() => {
  const daemonTarget = resolveDaemonTarget();

  return {
    base: './',
    plugins: [react()],
    server: {
      port: 4173,
      host: '127.0.0.1',
      proxy: {
        '/api': {
          target: daemonTarget,
          changeOrigin: true,
          ws: true,
        },
        '/internal': {
          target: daemonTarget,
          changeOrigin: true,
        },
      },
    },
  };
});
