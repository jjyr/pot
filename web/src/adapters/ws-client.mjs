import {createWSLifecycle} from '../state/ws-lifecycle.mjs';
const WS = (() => {
  const IDLE = 45000, CONNECT_TIMEOUT = 20000, SUB_TIMEOUT = 20000, REQ_TIMEOUT = 60000, MAX = 64;

  function createWSSession({endpoint, host = globalThis, timeout = REQ_TIMEOUT, maxPending = MAX, desktop = false, base = '', onDenied = () => {}}) {
    const state = {
      pending: new Map(), subs: new Set(), wire: new Map(),
      socket: null, generation: 0, connecting: false,
      connectTimer: null, idleTimer: null, retryTimer: null,
      closed: false, seq: 0, retryAttempt: 0, denied: null
    };
    const getNow = host.Date && typeof host.Date.now === 'function' ? () => host.Date.now() : () => Date.now();
    const work = () => state.subs.size > 0 || state.pending.size > 0;
    const lifecycle = createWSLifecycle({
      host, desktop, base, now: getNow,
      onResume: () => { if (work() && !state.closed) connect(); },
      onOffline: (reason) => { if (state.socket || state.connecting) disconnect(reason); },
      onDenied: (reason) => {
        state.denied = reason;
        const snap = Array.from(state.pending.values());
        state.pending.clear();
        for (const e of snap) {
          clearTimer(e.timer);
          if (e.abort) e.abort();
          e.reject(Object.assign(new Error(reason && reason.message || 'denied'), {...reason, unknown: false}));
        }
        for (const sub of [...state.subs]) {
          if (!state.subs.has(sub)) continue;
          clearTimer(sub.timer); sub.timer = null;
          try { sub.onStatus('offline', reason); } catch {}
        }
        clearTimer(state.retryTimer); state.retryTimer = null;
        try { onDenied(reason); } catch {}
      }
    });

    function clearTimer(t) { if (t) host.clearTimeout(t); }
    function nextId() { return String(++state.seq); }
    function scheduleIdle() {
      if (state.socket) lifecycle.activity();
    }
    function closeSocket() {
      state.generation++;
      state.connecting = false;
      clearTimer(state.connectTimer);
      clearTimer(state.idleTimer);
      state.connectTimer = null; state.idleTimer = null;
      const s = state.socket;
      state.socket = null;
      if (s) { try { s.onopen = s.onmessage = s.onerror = s.onclose = null; s.close(); } catch {} }
    }

    function disconnect(reason) {
      state.generation++;
      state.connecting = false;
      closeSocket();
      const generation = state.generation;
      const recovery = lifecycle.failed();
      const pendingSnapshot = Array.from(state.pending.values());
      state.pending.clear();
      for (const e of pendingSnapshot) {
        clearTimer(e.timer);
        if (e.abort) e.abort();
        e.reject(Object.assign(new Error('WebSocket disconnected', {cause: reason}), {status: 503, code: 'stream_disconnected', uiKey: 'connection.streamUnavailable', unknown: !!e.sent}));
      }
      state.wire.clear();
      for (const sub of [...state.subs]) {
        if (!state.subs.has(sub)) continue;
        clearTimer(sub.timer); sub.timer = null;
        sub.acked = false; sub.ready = false; sub.id = null;
        try { sub.onStatus('offline', Object.assign(new Error('WebSocket disconnected',{cause:reason}),{status:503,code:'stream_disconnected',uiKey:'connection.streamUnavailable'})); } catch {}
      }
      if (recovery && typeof recovery.then === 'function') {
        recovery.then((result) => {
          if (state.closed || generation !== state.generation) return;
          if (result && result.retry && (state.subs.size || state.pending.size)) scheduleRetry();
        }).catch(() => {});
      }
    }

    function scheduleRetry() {
      clearTimer(state.retryTimer);
      const attempt = state.retryAttempt = (state.retryAttempt || 0) + 1;
      const base = Math.min(30000, 500 * Math.pow(2, attempt - 1));
      const jitter = Math.min(30000, Math.floor(base * (0.8 + Math.random() * 0.4)));
      state.retryTimer = host.setTimeout(() => { state.retryTimer = null; if (!state.closed && (state.subs.size || state.pending.size) && lifecycle.canConnect()) connect(); }, jitter);
      if (state.retryTimer.unref) state.retryTimer.unref();
    }

    async function connect() {
      if (state.closed || state.connecting || state.socket) return;
      if (state.denied) return;
      if (!lifecycle.canConnect()) return;
      clearTimer(state.retryTimer); state.retryTimer = null;
      const gen = ++state.generation;
      state.connecting = true;
      state.connectTimer = host.setTimeout(() => { if (gen === state.generation) disconnect('connect timeout'); }, CONNECT_TIMEOUT);

      let info;
      try { info = await endpoint(); }
      catch (e) { if (gen === state.generation) { state.connecting = false; disconnect(e?.message || 'endpoint failed'); } return; }
      if (gen !== state.generation || state.closed) return;

      let ws;
      try {
        const protos = ['pot.v1'];
        if (info.token) protos.push('pot-token.' + info.token);
        ws = new host.WebSocket(info.url, protos);
      } catch (e) { state.connecting = false; disconnect('ws construct failed'); return; }

      state.socket = ws;

      ws.onopen = () => {
        if (gen !== state.generation) { try { ws.close(); } catch {} return; }
        if (ws.protocol !== 'pot.v1') { disconnect('protocol mismatch'); return; }
        state.connecting = false;
        clearTimer(state.connectTimer); state.connectTimer = null;
        for (const [, e] of [...state.pending]) {
          if (!e.sent && state.pending.get(e.id) === e && state.socket === ws && gen === state.generation) {
            e.sent = true;
            try { ws.send(JSON.stringify({type: 'request', id: e.id, method: e.method, path: e.path, body: e.body, expected_control_id: e.expected_control_id})); } catch (err) { disconnect(err?.message || 'send failed'); return; }
          }
        }
        for (const sub of [...state.subs]) activate(sub);
        scheduleIdle();
      };
      ws.onmessage = (ev) => { if (gen === state.generation) onMessage(ev); };
      ws.onerror = () => { if (gen === state.generation) disconnect('socket error'); };
      ws.onclose = () => { if (gen === state.generation) disconnect('socket closed'); };
    }

    function activate(sub) {
      if (!state.subs.has(sub) || !state.socket) return;
    if (sub.id && state.wire.get(sub.id) === sub) return;
      const sock = state.socket;
      const gen = state.generation;
      sub.id = nextId();
      sub.acked = false; sub.ready = false;
      state.wire.set(sub.id, sub);
      try { sub.onStatus('connecting'); } catch {}
      if (gen !== state.generation || !state.subs.has(sub) || state.socket !== sock || sock.readyState !== 1) return;
      clearTimer(sub.timer);
      sub.timer = host.setTimeout(() => { if (state.subs.has(sub) && !sub.ready) disconnect('subscribe timeout'); }, SUB_TIMEOUT);
      try { sock.send(JSON.stringify({type: 'subscribe', id: sub.id, path: sub.path, expected_control_id: sub.expected_control_id})); }
      catch { disconnect('subscribe send failed'); return; }
    }

function onMessage(ev) {
  let raw;
  try {
    raw = typeof ev.data === 'string' ? ev.data : new TextDecoder().decode(ev.data);
  } catch (e) { return disconnect(e && e.message || 'bad payload'); }
  const byteLen = new TextEncoder().encode(raw).length;
  if (byteLen > 16 * 1024 * 1024) return disconnect('frame too large');
  let frame;
  try { frame = JSON.parse(raw); }
  catch (e) { return disconnect(e && e.message || 'bad json'); }
  if (!frame || typeof frame !== 'object' || Array.isArray(frame)) return disconnect('bad frame');
  if (typeof frame.type !== 'string') return disconnect('bad frame type');

  scheduleIdle();

  if (frame.type === 'heartbeat') return;

  if (frame.type === 'response') {
    const id = frame.id;
    const e = state.pending.get(id);
    if (e) {
      state.pending.delete(id);
      clearTimer(e.timer);
      if (e.abort) e.abort();
      if (frame.error || !(frame.status >= 200 && frame.status < 300)) {
        const err = frame.error;
        const msg = (err && (err.message || err.code)) || (typeof err === 'string' ? err : 'request failed');
        const unknown = frame.status === 504 || (err && err.code === 'request_timeout');
      e.reject(Object.assign(new Error(msg), {status: frame.status, code: err && err.code, ...(unknown ? {unknown: true} : {})}));
      } else {
        e.resolve(frame.result);
      }
      scheduleIdle();
      return;
    }
    const sub = state.wire.get(id);
    if (sub) {
      if (frame.status === 200 && !frame.error) {
        sub.acked = true;
        return;
      }
      if (frame.status >= 400 && frame.status < 500) {
        state.wire.delete(id);
        state.subs.delete(sub);
        clearTimer(sub.timer); sub.timer = null;
        sub.acked = false; sub.ready = false; sub.id = null;
        try { sub.onStatus('offline', frame.error); } catch {}
        scheduleIdle();
        if (!state.subs.size && !state.pending.size) closeSocket();
        return;
      }
      return disconnect((frame.error && (frame.error.message || frame.error.code)) || 'subscribe failed');
    }
    if (frame.error && frame.status == null) return disconnect(frame.error.message || frame.error.code || 'server error');
    return;
  }

  if (frame.type === 'event') {
    const sub = state.wire.get(frame.id);
    if (!sub || !sub.acked) return;
    const parser = sub.events && sub.events[frame.event];
    if (typeof parser !== 'function') return;
    let accepted;
    try {
      accepted = parser({data: JSON.stringify(frame.data), lastEventId: frame.event_id || ''}, !sub.ready);
    } catch (e) { return disconnect(e); }
    if (accepted === false) return;
    if (state.wire.get(frame.id) !== sub) return;
    if (!sub.ready) {
      sub.ready = true;
      clearTimer(sub.timer); sub.timer = null;
      try { sub.onStatus('connected'); } catch (e) { throw e; }
    }
    if (state.wire.get(frame.id) !== sub || !state.subs.has(sub)) return;
    state.retryAttempt = 0;
    if (typeof accepted === 'function') accepted();
    return;
  }

  if (frame.type === 'end') {
    const sub = state.wire.get(frame.id);
    if (!sub) return;
    const status = frame.status != null ? frame.status : (frame.error && frame.error.status);
    if (status >= 400 && status < 500) {
      state.wire.delete(frame.id);
      state.subs.delete(sub);
      clearTimer(sub.timer); sub.timer = null;
      sub.acked = false; sub.ready = false; sub.id = null;
      try { sub.onStatus('offline', frame.error); } catch {}
      scheduleIdle();
      if (!state.subs.size && !state.pending.size) closeSocket();
      return;
    }
    return disconnect((frame.error && (frame.error.message || frame.error.code)) || 'stream ended');
  }

  return disconnect('unknown frame type');
}

    function request(path, {method = 'GET', body, signal, timeout: reqT, expected_control_id} = {}) {
      if (state.closed) return Promise.reject(new Error('session closed'));
      if (state.denied) return Promise.reject(Object.assign(new Error(state.denied && state.denied.message || 'denied'), state.denied));
      if (state.pending.size + state.subs.size >= maxPending) return Promise.reject(Object.assign(new Error('too many pending'), {status: 429, code: 'too_many_pending', uiKey: 'connection.streamUnavailable'}));
      if (signal && signal.aborted) return Promise.reject(signal.reason);
      const id = nextId();
      return new Promise((resolve, reject) => {
        const entry = {id, method, path, body, expected_control_id, resolve, reject, sent: false, timer: null, abort: null};
        const onAbort = () => {
          if (!state.pending.delete(id)) return;
          clearTimer(entry.timer);
          if (entry.sent && state.socket) { try { state.socket.send(JSON.stringify({type: 'cancel', id})); } catch {} }
          reject(signal.reason);
        };
        if (signal) { signal.addEventListener('abort', onAbort, {once: true}); entry.abort = () => signal.removeEventListener('abort', onAbort); }
        entry.timer = host.setTimeout(() => {
          if (!state.pending.delete(id)) return;
          if (entry.abort) entry.abort();
          if (entry.sent && state.socket) { try { state.socket.send(JSON.stringify({type: 'cancel', id})); } catch {} }
          reject(Object.assign(new Error('request timeout'), {status: 504, code: 'timeout', uiKey: 'connection.streamUnavailable', unknown: !!entry.sent}));
        }, reqT || timeout);
        state.pending.set(id, entry);
        if (state.socket && state.socket.readyState === 1) {
          entry.sent = true;
          try { state.socket.send(JSON.stringify({type: 'request', id, method, path, body, expected_control_id})); }
          catch (err) { disconnect(err?.message || 'send failed'); }
        } else connect();
      });
    }

    function subscribe(path, {events = {}, onStatus = () => {}, expected_control_id} = {}) {
      if (state.closed) { onStatus('offline', { status: 503, code: 'session_closed', uiKey: 'connection.streamUnavailable' }); return () => {}; }
      if (state.denied) { try { onStatus('offline', state.denied); } catch {} return () => {}; }
      if (state.pending.size + state.subs.size >= maxPending) {
        const err = Object.assign(new Error('too many pending'), {status: 429, code: 'too_many_pending', uiKey: 'connection.streamUnavailable'});
        try { onStatus('offline', err); } catch {}
        return () => {};
      }
      const sub = {path, events, onStatus, expected_control_id, id: null, ready: false, timer: null, acked: false};
      state.subs.add(sub);
      if (state.socket && state.socket.readyState === 1) activate(sub);
      else connect();
      return function unsubscribe() {
        if (!state.subs.delete(sub)) return;
        if (sub.id) {
          const w = state.wire.get(sub.id);
          if (w === sub) state.wire.delete(sub.id);
          if (state.socket && state.socket.readyState === 1) { try { state.socket.send(JSON.stringify({type: 'unsubscribe', id: sub.id})); } catch {} }
        }
        clearTimer(sub.timer); sub.timer = null;
        if (!state.subs.size && !state.pending.size) closeSocket();
      };
    }

    function close() {
      state.closed = true;
      try { lifecycle.close(); } catch {}
      state.generation++;
      state.connecting = false;
      closeSocket();
      clearTimer(state.retryTimer); state.retryTimer = null;
      for (const [, e] of state.pending) { clearTimer(e.timer); if (e.abort) e.abort(); e.reject(new Error('session closed')); }
      state.pending.clear();
      for (const sub of [...state.subs]) {
        if (!state.subs.has(sub)) continue;
        clearTimer(sub.timer);
        sub.timer = null;
        try { sub.onStatus('offline'); } catch {}
      }
      state.subs.clear(); state.wire.clear();
    }

    return {request, subscribe, close, confirmAccess(){if(state.closed)return;state.denied=null;lifecycle.accessConfirmed();}};
  }

const cache = new Map();
function getBusinessSocket({ desktop = false, base = '' } = {}) {
  const key = JSON.stringify([desktop, base]);
  let session = cache.get(key);
  if (session) return session;
  const endpoint = desktop
    ? async () => {
        const api = await import('/wails/runtime.js');
        return api.Call.ByName('github.com/jjyr/pot/internal/desktop.Service.Transport');
      }
    : async () => {
        const url = new URL(base + '/api/v1/ws', globalThis.location?.href || 'http://localhost/');
        url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
        return { url: url.href };
      };
  session = createWSSession({endpoint,desktop,base});
  cache.set(key, session);
  return session;
}
async function readBusinessStartup({ desktop = false, base = '', signal } = {}) {
  const runtime = await getBusinessSocket({ desktop, base }).request('/api/v1/runtime', { signal });
  if (!(runtime?.runtime_id || runtime?.id) || !runtime.boot_id)
    throw new Error('invalid runtime');
  if (!desktop) return { runtime };
  if (signal?.throwIfAborted) signal.throwIfAborted();
  const api = await import('/wails/runtime.js');
  let onAbort;
  const aborted = new Promise((_, reject) => {
    if (!signal) return;
    onAbort = () => reject(signal.reason ?? new DOMException('aborted', 'AbortError'));
    if (signal.aborted) onAbort();
    else signal.addEventListener('abort', onAbort, { once: true });
  });
  let readBaselines;
  try {
    readBaselines = await Promise.race([
      api.Call.ByName('github.com/jjyr/pot/internal/desktop.Service.AssistantReads'),
      aborted,
    ]);
  } finally {
    if (onAbort) signal.removeEventListener('abort', onAbort);
  }
  return { runtime, readBaselines };
}
globalThis.addEventListener?.('pagehide', (e) => {
  if (e.persisted) return;
  for (const s of cache.values()) { try { s.close?.(); } catch {} }
  cache.clear();
});

  function confirmBusinessAccess({base=''}={}){cache.get(JSON.stringify([false,base]))?.confirmAccess();}
  return {createWSSession, getBusinessSocket, readBusinessStartup, confirmBusinessAccess};
})();

const {createWSSession, getBusinessSocket, readBusinessStartup, confirmBusinessAccess} = WS;
export {createWSSession, getBusinessSocket, readBusinessStartup, confirmBusinessAccess};
