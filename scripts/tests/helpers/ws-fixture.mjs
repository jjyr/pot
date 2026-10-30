export function installWSFixture() {
  const oldWS = globalThis.WebSocket;
  const sockets = [];
  class FakeWebSocket {
    static OPEN = 1; static CLOSED = 3;
    constructor(url, protocols) {
      this.url = url;
      this.protocols = protocols;
      this.protocol = 'pot.v1';
      this.readyState = 0;
      this.sent = [];
      this.subscriptions = new Map();
      this._controllers = new Map();
      queueMicrotask(() => { if (this.readyState === 0) { this.readyState = 1; this.onopen?.({}); } });
      sockets.push(this);
    }
    _fire(frame) { this.onmessage?.({ data: JSON.stringify(frame) }); }
    send(raw) {
      const frame = JSON.parse(raw); this.sent.push(frame);
      if (frame.type === 'request') {
        const ctl = new AbortController(); this._controllers.set(frame.id, ctl);
        (async () => {
          let res;
          try {
            const r = await globalThis.fetch(frame.path, { method: frame.method, body: frame.body === undefined ? undefined : JSON.stringify(frame.body), signal: ctl.signal });
            const json = r.status === 204 ? {} : await r.json().catch(() => ({}));
            res = r.ok ? { status: r.status, result: json } : { status: r.status, error: json };
          } catch (e) {
            res = { status: 500, error: { code: 'fixture_error', message: String(e && e.message || e) } };
          } finally {
            this._controllers.delete(frame.id);
          }
          if (this.readyState === 3) return;
          this._fire({ type: 'response', id: frame.id, ...res });
        })();
      } else if (frame.type === 'cancel') {
        this._controllers.get(frame.id)?.abort(); this._controllers.delete(frame.id);
      } else if (frame.type === 'subscribe') {
        this.subscriptions.set(frame.id, frame.path); this._fire({ type: 'response', id: frame.id, status: 200, result: {} });
      } else if (frame.type === 'unsubscribe') {
        this.subscriptions.delete(frame.id); this._fire({ type: 'response', id: frame.id, status: 200, result: {} });
      }
    }
    emit(frame) { this._fire(frame); }
    close() {
      if (this.readyState === 3) return; this.readyState = 3;
      for (const c of this._controllers.values()) c.abort(); this._controllers.clear();
      this.subscriptions.clear();
      this.onclose?.({});
    }
  }
  globalThis.WebSocket = FakeWebSocket;
  const flush = async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); };
  const emit = (match, event, data, event_id = 'boot:1') => {
    for (const s of sockets) {
      if (s.readyState !== 1) continue;
      for (const [id, path] of s.subscriptions) {
        if (path.includes(match)) s.emit({ type: 'event', id, event, data, event_id });
      }
    }
  };
  const restore = () => { for (const s of sockets) s.close(); globalThis.WebSocket = oldWS; };
  return { sockets, flush, emit, restore, WebSocket: FakeWebSocket };
}
