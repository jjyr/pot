import assert from 'node:assert';

export function wsClock() {
  const sockets = [];
  let now = 0;
  let id = 0;
  const timers = new Map();

  class WebSocket {
    constructor(url, protocols) {
      this.url = url;
      this.protocols = protocols;
      this.protocol = 'pot.v1';
      this.readyState = 0;
      this.sent = [];
      sockets.push(this);
    }
    open() {
      this.readyState = 1;
      this.onopen?.();
    }
    send(raw) {
      this.sent.push(JSON.parse(raw));
    }
    emit(frame) {
      this.onmessage?.({ data: JSON.stringify(frame) });
    }
    close() {
      this.readyState = 3;
    }
    serverClose() {
      this.readyState = 3;
      this.onclose?.();
    }
  }

  const host = {
    WebSocket,
    setTimeout(fn, ms) {
      const t = ++id;
      timers.set(t, { due: now + ms, fn });
      return t;
    },
    clearTimeout(t) {
      timers.delete(t);
    },
    Date: { now: () => now },
  };

  async function tick() {
    await new Promise((r) => setTimeout(r, 10));
    await Promise.resolve();
  }

  async function advance(ms) {
    const end = now + ms;
    let fired = 0;
    try {
      for (;;) {
        let best = null;
        for (const [t, timer] of timers) {
          if (timer.due <= end && (!best || timer.due < best.timer.due)) {
            best = { t, timer };
          }
        }
        if (!best) break;
        assert.ok(++fired <= 100, 'wsClock advance: too many timers');
        now = best.timer.due;
        timers.delete(best.t);
        best.timer.fn();
        await tick();
      }
    } finally {
      now = end;
    }
  }

  return { host, sockets, tick, advance };
}
