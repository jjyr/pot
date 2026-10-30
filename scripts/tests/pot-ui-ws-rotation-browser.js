async page => {
  const state = STATE;

  // Authenticate before navigation so the page picks up the session.
  const tokenResp = await page.request.post(state.origin + '/access/token', {
    headers: { Authorization: 'Bearer ' + state.token },
  });
  if (tokenResp.status() !== 200) {
    throw new Error('authentication failed with status ' + tokenResp.status());
  }

  await page.goto(state.origin);

  try {
    // Phase 1: open the native WebSocket, verify providers response hides the
    // isolated key, subscribe to changes, and wait for a real "changed" event.
    const ready = await page.evaluate(async () => {
      const wsUrl = location.origin.replace(/^http/, 'ws') + '/api/v1/ws';

      return await new Promise((resolve, reject) => {
        let readyTimer = null;
        let settled = false;

        const socket = new WebSocket(wsUrl, 'pot.v1');
        window.__rotationSocket = socket;

        window.__rotationClosed = new Promise((res) => {
          socket.addEventListener('close', res, { once: true });
        });

        const finish = (fn, value) => {
          if (settled) return;
          settled = true;
          if (readyTimer !== null) {
            clearTimeout(readyTimer);
            readyTimer = null;
          }
          fn(value);
        };

        readyTimer = setTimeout(() => {
          finish(reject, new Error('websocket readiness timed out'));
        }, 10000);

        socket.addEventListener('error', () => {
          finish(reject, new Error('websocket error before ready'));
        });

        socket.addEventListener('close', () => {
          finish(reject, new Error('websocket closed before ready'));
        });

        socket.addEventListener('open', () => {
          socket.send(
            JSON.stringify({
              type: 'request',
              id: 'providers',
              method: 'GET',
              path: '/api/v1/providers',
            })
          );
        });

        socket.addEventListener('message', (event) => {
          let msg;
          try {
            msg = JSON.parse(event.data);
          } catch (e) {
            return;
          }

          if (msg.type === 'response' && msg.id === 'providers') {
            if (msg.status !== 200) {
              finish(reject, new Error('providers response status ' + msg.status));
              return;
            }
            let serialized;
            try {
              serialized = JSON.stringify(msg.result);
            } catch (e) {
              finish(reject, new Error('providers result not serializable'));
              return;
            }
            if (typeof serialized === 'string' && serialized.includes('isolated-key')) {
              finish(reject, new Error('provider secret leaked in providers response'));
              return;
            }
            socket.send(
              JSON.stringify({
                type: 'subscribe',
                id: 'changes',
                path: '/api/v1/changes',
              })
            );
            return;
          }

          if (msg.type === 'response' && msg.id === 'changes') {
            if (msg.error || (typeof msg.status === 'number' && msg.status >= 400)) {
              finish(reject, new Error('changes subscribe failed'));
            }
            return;
          }

          if (msg.type === 'event' && msg.id === 'changes') {
            if (msg.event === 'changed') {
              finish(resolve, true);
            }
            return;
          }

          // Ignore heartbeat and anything else.
        });
      });
    });

    if (ready !== true) {
      throw new Error('websocket readiness did not resolve');
    }

    // Phase 2: coordinator rotates, revoking the active socket.
    const rotateResp = await page.request.post(state.coord + '/rotate', {
      headers: { Authorization: 'Bearer ' + state.control_key },
    });
    if (rotateResp.status() !== 200) {
      throw new Error('rotation failed with status ' + rotateResp.status());
    }

    // Phase 3: the previously open socket must be closed.
    const closed = await page.evaluate(async () => {
      let timer = null;
      try {
        return await Promise.race([
          window.__rotationClosed.then(() => true),
          new Promise((resolve) => {
            timer = setTimeout(() => resolve(false), 5000);
          }),
        ]);
      } finally {
        if (timer !== null) {
          clearTimeout(timer);
        }
      }
    });

    if (closed !== true) {
      throw new Error('active websocket was not revoked after rotation');
    }

    return {
      passed: true,
      active_ws_revoked: true,
      provider_secret_hidden: true,
    };
  } finally {
    // Always clean up the saved socket and globals, even on failure.
    await page.evaluate(async () => {
      const socket = window.__rotationSocket;
      if (socket) {
        try {
          if (socket.readyState === 0 || socket.readyState === 1) {
            socket.close();
          }
        } catch (e) {
          // ignore close errors
        }
        // Give the close a moment to settle when possible.
        await new Promise((resolve) => {
          let timer = null;
          const done = () => {
            if (timer !== null) {
              clearTimeout(timer);
              timer = null;
            }
            resolve();
          };
          try {
            if (socket.readyState === 3) {
              done();
              return;
            }
            socket.addEventListener('close', done, { once: true });
            timer = setTimeout(done, 1000);
          } catch (e) {
            done();
          }
        });
      }
      try {
        delete window.__rotationSocket;
      } catch (e) {
        // ignore
      }
      try {
        delete window.__rotationClosed;
      } catch (e) {
        // ignore
      }
    });
  }
}
