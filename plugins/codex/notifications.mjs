import {setTimeout as delay} from 'node:timers/promises';
import {pairing, request, frames} from './client.mjs';

const critical = event => event.kind === 'terminal' || event.kind === 'approval' || event.kind === 'message' || event.kind === 'cancellation' && event.data.status === 'cancelled';
const envelope = (pair, event) => ({id: `${pair.id}:${event.id}`, title: 'Codex', body: JSON.stringify({kind: event.kind, ...event.data}), occurred_at: event.at});

export async function stream(input, emit, options = {}) {
  const pair = options.pair || pairing(), call = options.request || request, sleep = options.delay || delay;
  let state = input.state?.pair === pair.id ? structuredClone(input.state) : {pair: pair.id, cursor: 0, acks: []};
  let failures = 0;
  const commit = async (cursor, records) => {
    const acks = records.filter(e => ['terminal', 'approval', 'message', 'cancellation'].includes(e.kind)).map(e => e.id);
    const next = {pair: pair.id, cursor, acks};
    const events = records.filter(critical).map(e => envelope(pair, e));
    await emit({state: next, events, delay_seconds: 60, status: 'ready'});
    state = next;
    await acknowledge();
  };
  const acknowledge = async () => {
    for (const id of state.acks || []) await call(pair, `/v1/receipts/${id}`, {method: 'POST', body: {}});
    state.acks = [];
  };
  const resync = async () => {
    for (;;) {
    const snapshot = await call(pair, '/v1/snapshot');
    if (snapshot.pair !== pair.id || snapshot.route !== pair.route || !Number.isSafeInteger(snapshot.cursor) || !Array.isArray(snapshot.notices) || snapshot.notices.length > 256) throw Error('invalid_snapshot');
    for (let i = 0; i < snapshot.notices.length; i += 8) await commit(state.cursor, snapshot.notices.slice(i, i + 8));
    if (snapshot.more) continue;
    await commit(snapshot.cursor, []);
    return;
    }
  };
  for (;;) {
    let response;
    try {
      await acknowledge();
      if (!state.cursor) await resync();
      response = await call(pair, `/v1/events?after=${state.cursor}`, {stream: true, headers: {'Last-Event-ID': String(state.cursor)}});
      for await (const event of frames(response)) {
        if (event.cursor <= state.cursor) continue;
        // Persist high-frequency progress only as the cursor. It never wakes a model.
        await commit(event.cursor, [event]); failures = 0;
      }
    } catch (error) {
      if (options.signal?.aborted) return;
      if (error.status === 409) { await resync(); continue; }
      const status = error.status === 401 ? 'authorization' : error.status === 429 ? 'rate_limited' : 'backoff';
      await emit({state, events: [], delay_seconds: 60, status});
      failures++;
      await sleep(Math.min(60000, 1000 * 2 ** Math.min(failures, 6)) + Math.floor(Math.random() * 500));
    } finally { response?.destroy(); }
  }
}
