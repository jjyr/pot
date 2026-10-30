// TEST ONLY: shared WS framing adapter reusing synthetic fetch+EventSource.
(function () {
  'use strict';

  class FixtureWS extends EventTarget {
    static CONNECTING = 0;
    static OPEN = 1;
    static CLOSING = 2;
    static CLOSED = 3;

    constructor(url, protocols) {
      super();
      this.url = url;
      this.protocol = 'pot.v1';
      this.readyState = FixtureWS.CONNECTING;
      this.controllers = new Map();
      this.sources = new Map();
      this._hb = null;
      this._nextId = 1;
      Promise.resolve().then(() => {
        if (this.readyState !== FixtureWS.CONNECTING) return;
        this.readyState = FixtureWS.OPEN;
        const ev = new Event('open');
        this._dispatch(ev);
        this._startHeartbeat();
      });
    }

    _startHeartbeat() {
      this._stopHeartbeat();
      this._hb = setInterval(() => {
        if (this.readyState !== FixtureWS.OPEN) return;
        this._emit({ type: 'heartbeat' });
      }, 15000);
    }

    _stopHeartbeat() {
      if (this._hb) {
        clearInterval(this._hb);
        this._hb = null;
      }
    }

    _dispatch(event) {
      if (typeof this['on' + event.type] === 'function') {
        this['on' + event.type](event);
      }
      this.dispatchEvent(event);
    }

    _emit(data) {
      if (this.readyState !== FixtureWS.OPEN) return;
      const payload = JSON.stringify(data);
      setTimeout(() => {
        if (this.readyState !== FixtureWS.OPEN) return;
        const event = new MessageEvent('message', { data: payload });
        this._dispatch(event);
      }, 0);
    }

    send(raw) {
      let frame;
      try {
        frame = JSON.parse(raw);
      } catch (e) {
        return;
      }
      if (!frame || typeof frame !== 'object') return;
      switch (frame.type) {
        case 'request':
          this._handleRequest(frame);
          break;
        case 'subscribe':
          this._handleSubscribe(frame);
          break;
        case 'unsubscribe':
          this._handleUnsubscribe(frame);
          break;
        case 'cancel':
          this._handleCancel(frame);
          break;
        default:
          break;
      }
    }

    _handleRequest(frame) {
      const id = frame.id;
      const controller = new AbortController();
      this.controllers.set(id, controller);
      const body =
        frame.body !== undefined && typeof frame.body !== 'undefined'
          ? JSON.stringify(frame.body)
          : undefined;
      const init = {
        method: frame.method || 'GET',
        credentials: 'same-origin',
        headers: { 'content-type': 'application/json' },
        signal: controller.signal,
      };
      if (body !== undefined) init.body = body;
      const run = async () => {
        try {
          const response = await fetch(frame.path, init);
          if (response.status === 204) {
            if (this.readyState === FixtureWS.OPEN && !controller.signal.aborted) {
              this._emit({ type: 'response', id, status: 204, result: null });
            }
            return;
          }
          let result = null;
          if (response.ok) {
            try {
              result = await response.json();
            } catch (e) {
              result = null;
            }
          } else {
            try {
              result = await response.json();
            } catch (e) {
              result = { code: 'fixture_error' };
            }
          }
          if (this.readyState === FixtureWS.OPEN && !controller.signal.aborted) {
            if (response.ok) {
              this._emit({ type: 'response', id, status: response.status, result });
            } else {
              this._emit({ type: 'response', id, status: response.status, error: result });
            }
          }
        } catch (err) {
          if (controller.signal.aborted) return;
          if (this.readyState === FixtureWS.OPEN) {
            this._emit({
              type: 'response',
              id,
              status: 503,
              error: { code: 'fixture_error' },
            });
          }
        } finally {
          if (this.controllers.get(id) === controller) {
            this.controllers.delete(id);
          }
        }
      };
      run();
    }

    _handleSubscribe(frame) {
      const id = frame.id;
      if (this.readyState !== FixtureWS.OPEN) return;
      this._emit({ type: 'response', id, status: 200, result: {} });
      const source = new EventSource(frame.path);
      this.sources.set(id, source);
      const forward = (name) => (e) => {
        if (this.sources.get(id) !== source) return;
        if (this.readyState !== FixtureWS.OPEN) return;
        let data = null;
        try {
          data = e.data ? JSON.parse(e.data) : null;
        } catch (err) {
          data = null;
        }
        this._emit({
          type: 'event',
          id,
          event: name,
          event_id: e.lastEventId || '',
          data,
        });
      };
      const events = ['conversation', 'changed', 'changes', 'message', 'snapshot'];
      for (const name of events) {
        source.addEventListener(name, forward(name));
      }
      source.addEventListener('heartbeat', () => {
        if (this.sources.get(id) !== source) return;
        if (this.readyState !== FixtureWS.OPEN) return;
        this._emit({ type: 'heartbeat' });
      });
      source.onerror = () => {
        if (this.sources.get(id) !== source) return;
        this.close();
      };
    }

    _handleUnsubscribe(frame) {
      const id = frame.id;
      const source = this.sources.get(id);
      if (source) {
        try { source.close(); } catch (e) { /* test-only */ }
        this.sources.delete(id);
      }
      if (this.readyState === FixtureWS.OPEN) {
        this._emit({ type: 'response', id, status: 200, result: {} });
      }
    }

    _handleCancel(frame) {
      const id = frame.id;
      const controller = this.controllers.get(id);
      if (controller) {
        try { controller.abort(); } catch (e) { /* test-only */ }
        this.controllers.delete(id);
      }
    }

    close() {
      if (this.readyState === FixtureWS.CLOSED) return;
      this.readyState = FixtureWS.CLOSED;
      this._stopHeartbeat();
      for (const [, controller] of this.controllers) {
        try { controller.abort(); } catch (e) { /* test-only */ }
      }
      this.controllers.clear();
      for (const [, source] of this.sources) {
        try { source.close(); } catch (e) { /* test-only */ }
      }
      this.sources.clear();
      const ev = new CloseEvent('close', { code: 1000 });
      this._dispatch(ev);
    }
  }

  if (typeof window !== 'undefined') {
    window.WebSocket = FixtureWS;
  }
})();
