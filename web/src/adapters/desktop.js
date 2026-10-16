// Only this adapter knows about the Wails runtime. Product components share the
// same client contract with the HTTP/SSE browser host.
import {Call, Events} from '/wails/runtime.js';

const service = 'github.com/jjyr/pot/internal/desktop.PreviewService';
const invoke = (method, ...args) => Call.ByName(`${service}.${method}`, ...args);

export function createDesktopClient() {
  return {
    snapshot: ({signal} = {}) => {
      if (signal?.aborted) return Promise.reject(signal.reason);
      const pending = invoke('Snapshot');
      const abort = () => pending.cancel();
      signal?.addEventListener('abort', abort, {once: true});
      return pending.finally(() => signal?.removeEventListener('abort', abort));
    },
    send: request => invoke('Send', request),
    cancel: request => invoke('Cancel', request),
    openMain: () => invoke('OpenMain'),
    subscribe(onSnapshot, onStatus = () => {}) {
      const stop = Events.On('preview:snapshot', event => onSnapshot(event.data));
      // Read after subscribing; reducers reject a snapshot older than an event.
      let active = true;
      invoke('Snapshot').then(value => {
        if (active) { onSnapshot(value); onStatus('connected'); }
      }).catch(() => { if (active) onStatus('offline'); });
      return () => { active = false; stop(); };
    },
  };
}
