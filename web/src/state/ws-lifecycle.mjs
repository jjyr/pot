export function createWSLifecycle({
  host = globalThis,
  page = host.document,
  desktop = false,
  base = '',
  onResume = () => {},
  onOffline = () => {},
  onDenied = () => {},
  now = () => Date.now(),
  idleMs = 45000,
  probeMs = 5000,
} = {}) {
  let closed = false;
  let paused = false;
  let hidden = isHidden();
  let activeAt = 0;
  let hasActive = false;
  let idleTimer = null;
  let resumeTimer = null;
  let terminal = null;
  let deniedCalled = false;
  let probe = null;
  let probeSeq = 0;
  let browserOnline = desktop ? true : isNavigatorOnline();
  const disposers = [];

  function isNavigatorOnline() {
    return !(host.navigator && host.navigator.onLine === false);
  }

  function isHidden() {
    if (!page) return false;
    return page.hidden === true || page.visibilityState === 'hidden';
  }

  function clearIdleTimer() {
    if (idleTimer !== null) {
      host.clearTimeout(idleTimer);
      idleTimer = null;
    }
  }

  function clearResumeTimer() {
    if (resumeTimer !== null) {
      host.clearTimeout(resumeTimer);
      resumeTimer = null;
    }
  }

  function canConnect() {
    if (closed || paused || hidden) return false;
    if (probe) return false;
    if (!desktop && (!browserOnline || !isNavigatorOnline())) return false;
    return true;
  }

  function browserOnlineNow() {
    return desktop ? true : (browserOnline && isNavigatorOnline());
  }

  function scheduleIdle() {
    clearIdleTimer();
    if (closed || hidden || paused || !hasActive) return;
    if (!desktop && (!browserOnline || !isNavigatorOnline())) return;
    const elapsed = now() - activeAt;
    const remaining = idleMs - elapsed;
    idleTimer = host.setTimeout(checkIdle, remaining > 0 ? remaining : 0);
    if (idleTimer && typeof idleTimer.unref === 'function') idleTimer.unref();
  }

  function checkIdle() {
    idleTimer = null;
    if (closed || hidden || paused || !hasActive) return;
    const elapsed = now() - activeAt;
    if (elapsed < idleMs) {
      scheduleIdle();
      return;
    }
    hasActive = false;
    onOffline({ code: 'stream_idle_timeout', uiKey: 'streamDisconnected' });
    scheduleResume();
  }

  function scheduleResume() {
    if (closed || paused || hidden) return;
    if (!desktop && (!browserOnline || !isNavigatorOnline())) return;
    if (resumeTimer !== null) return;
    resumeTimer = host.setTimeout(() => {
      resumeTimer = null;
      if (closed || paused || hidden) return;
      if (!canConnect()) return;
      onResume();
    }, 0);
    if (resumeTimer && typeof resumeTimer.unref === 'function') resumeTimer.unref();
  }

  function activity() {
    if (closed || paused) return;
    activeAt = now();
    hasActive = true;
    if (!hidden && !paused) scheduleIdle();
    else clearIdleTimer();
  }

  function cancelProbe(result) {
    if (!probe) return;
    const p = probe;
    probe = null;
    p.settle(result, true);
  }

  function triggerIdle() {
    clearIdleTimer();
    if (!hasActive) {
      scheduleResume();
      return;
    }
    hasActive = false;
    onOffline({ code: 'stream_idle_timeout', uiKey: 'streamDisconnected' });
    scheduleResume();
  }

  function foreground() {
    if (closed || paused) return;
    hidden = isHidden();
    if (hidden) return;
    if (!desktop) browserOnline = isNavigatorOnline();
    if (!browserOnlineNow()) return;
    if (hasActive && now() - activeAt >= idleMs) {
      triggerIdle();
      return;
    }
    if (hasActive) scheduleIdle();
    scheduleResume();
  }

  function startProbe() {
    const gen = ++probeSeq;
    let resolve;
    const promise = new Promise(r => {
      resolve = r;
    });
    let settled = false;
    let timer = null;
    let controller = null;
    const AC = host.AbortController || globalThis.AbortController;
    if (typeof AC === 'function') {
      try {
        controller = new AC();
      } catch {
        controller = null;
      }
    }

    const settle = (result, abort = false) => {
      if (settled) return;
      settled = true;
      if (timer !== null) {
        host.clearTimeout(timer);
        timer = null;
      }
      if (abort && controller) {
        try {
          controller.abort();
        } catch {}
      }
      if (probe && probe.gen === gen) probe = null;
      resolve(result);
    };

    const record = { gen, settle };
    probe = record;

    timer = host.setTimeout(() => {
      if (settled) return;
      if (probe && probe.gen === gen) {
        if (controller) {
          try {
            controller.abort();
          } catch {}
        }
        settle({ retry: true });
      }
    }, probeMs);
    if (timer && typeof timer.unref === 'function') timer.unref();

    let fetchPromise;
    try {
      const init = { credentials: 'same-origin', cache: 'no-store', redirect: 'error' };
      if (controller) init.signal = controller.signal;
      fetchPromise = host.fetch.call(host, base + '/access/status', init);
    } catch {
      settle({ retry: true });
      return promise;
    }

    Promise.resolve(fetchPromise).then(
      resp => {
        if (settled || !probe || probe.gen !== gen || closed) return;
        if (resp && resp.status === 401) {
          const result = { retry: false, status: 401, code: 'unauthorized', uiKey: 'connection.authenticationRequired' };
          terminal = result;
          paused = true;
          settle(result);
          if (!deniedCalled) {
            deniedCalled = true;
            onDenied(result);
          }
          return;
        }
        if (resp && resp.status === 403) {
          const result = { retry: false, status: 403, code: 'forbidden', uiKey: 'connection.streamUnavailable' };
          terminal = result;
          paused = true;
          settle(result);
          if (!deniedCalled) {
            deniedCalled = true;
            onDenied(result);
          }
          return;
        }
        if (!resp || resp.status !== 200) {
          settle({ retry: true });
          return;
        }
        let jsonPromise;
        try {
          jsonPromise = resp.json();
        } catch {
          settle({ retry: true });
          return;
        }
        Promise.resolve(jsonPromise).then(
          data => {
            if (settled || !probe || probe.gen !== gen || closed) return;
            if (data && data.authenticated === false) {
              const result = { retry: false, status: 401, code: 'unauthorized', uiKey: 'connection.authenticationRequired' };
              terminal = result;
              paused = true;
              settle(result);
              if (!deniedCalled) {
                deniedCalled = true;
                onDenied(result);
              }
            } else {
              settle({ retry: true });
            }
          },
          () => {
            if (!settled && probe && probe.gen === gen && !closed) settle({ retry: true });
          }
        );
      },
      () => {
        if (!settled && probe && probe.gen === gen && !closed) settle({ retry: true });
      }
    );

    return promise;
  }

  function failed() {
    if (closed) return Promise.resolve({ retry: false, stale: true });
    clearIdleTimer();
    hasActive = false;
    activeAt = 0;
    if (terminal) {
      cancelProbe({ retry: false, stale: true });
      return Promise.resolve(terminal);
    }
    if (probe) cancelProbe({ retry: false, stale: true });
    if (desktop) return Promise.resolve({ retry: true });
    if (hidden || !browserOnline || !isNavigatorOnline()) return Promise.resolve({ retry: true });
    if (typeof host.fetch !== 'function') return Promise.resolve({ retry: true });
    return startProbe();
  }

  function handleVisibility() {
    const next = isHidden();
    if (next) {
      if (!hidden) {
        hidden = true;
        clearIdleTimer();
        clearResumeTimer();
        cancelProbe({ retry: false, stale: true });
      }
      return;
    }
    hidden = false;
    foreground();
  }

  function handleOffline() {
    if (desktop || closed) return;
    const wasOnline = browserOnline;
    browserOnline = false;
    cancelProbe({ retry: false, stale: true });
    clearIdleTimer();
    clearResumeTimer();
    if (wasOnline) onOffline({ code: 'stream_offline', uiKey: 'streamDisconnected' });
  }

  function handleOnline() {
    if (closed) return;
    browserOnline = true;
    foreground();
  }

  function addListener(target, type, fn) {
    if (target && typeof target.addEventListener === 'function') {
      target.addEventListener(type, fn);
      if (typeof target.removeEventListener === 'function') {
        disposers.push(() => target.removeEventListener(type, fn));
      }
    }
  }

  addListener(host, 'online', handleOnline);
  addListener(host, 'offline', handleOffline);
  addListener(host, 'focus', foreground);
  addListener(host, 'pageshow', foreground);
  addListener(page, 'visibilitychange', handleVisibility);

  function accessConfirmed() {
    if (closed) return;
    cancelProbe({ retry: false, stale: true });
    terminal = null;
    paused = false;
    deniedCalled = false;
  }

  function close() {
    if (closed) return;
    closed = true;
    clearIdleTimer();
    clearResumeTimer();
    cancelProbe({ retry: false, stale: true });
    while (disposers.length) {
      try {
        disposers.pop()();
      } catch {}
    }
  }

  return { canConnect, activity, failed, accessConfirmed, close };
}
