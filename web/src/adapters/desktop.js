import {Call} from '/wails/runtime.js';
import {createWSSession} from './ws-client.mjs';

const service = 'github.com/jjyr/pot/internal/desktop.PreviewService';

function invoke(method, ...args) {
  return Call.ByName(service + '.' + method, ...args);
}

export function createDesktopClient() {
  const socket = createWSSession({ desktop: true,
    endpoint: () => invoke('Transport'),
  });

  window.addEventListener('pagehide', (event) => {
    if (!event.persisted) {
      socket.close();
    }
  });

  return {
    scope: () => ({}),
    snapshot: (options) => socket.request('/api/preview/snapshot', options),
    send: (body) =>
      socket.request('/api/preview/messages', {method: 'POST', body}),
    cancel: (body) =>
      socket.request('/api/preview/cancel', {method: 'POST', body}),
    openMain: () => invoke('OpenMain'),
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
