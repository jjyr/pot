import { createWSSession } from './ws-client.mjs';

export function createBrowserClient(base = '') {
  const endpoint = async () => {
    const url = new URL(base + '/api/preview/ws', globalThis.location?.href || 'http://localhost/');
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    return { url: url.toString() };
  };
  const socket = createWSSession({ endpoint, base });

  if (typeof globalThis.addEventListener === 'function') {
    globalThis.addEventListener('pagehide', (event) => {
      if (!event.persisted) socket.close();
    });
  }

  return {
    scope: () => ({}),
    snapshot: (options) => socket.request('/api/preview/snapshot', options),
    send: (value) => socket.request('/api/preview/messages', { method: 'POST', body: value }),
    cancel: (value) => socket.request('/api/preview/cancel', { method: 'POST', body: value }),
    subscribe: (onSnapshot, onStatus = () => {}) =>
      socket.subscribe('/api/preview/events', {
        events: {
          snapshot: (event) => {
            const value = JSON.parse(event.data);
            return () => onSnapshot(value);
          },
        },
        onStatus,
      }),
    close: () => socket.close(),
  };
}
