// An editor belongs to its captured route/Space/owner for its entire lifetime.
// Request IDs persist through an unchanged retry; credentials stay memory-only.
export function createEditorSession(context, initial) {
  let baseline = structuredClone(initial), values = structuredClone(initial), pending = false;
  let retry, initialID = crypto.randomUUID();
  return {context: structuredClone(context),
    get requestID() { return retry?.id || initialID; },
    get values() { return values; }, get pending() { return pending; },
    get dirty() { return JSON.stringify(values) !== JSON.stringify(baseline); },
    set(name, value) { if (pending) return; values[name] = value; },
    begin() {
      if (pending) return null;
      pending = true;
      const content = JSON.stringify(values);
      if (!retry || retry.content !== content) retry = {id: retry ? crypto.randomUUID() : initialID, content};
      return {request_id: retry.id, values: structuredClone(values)};
    },
    acknowledge(fields) { Object.assign(baseline, structuredClone(fields)); },
    settle(accepted, next = values) {
      pending = false;
      if (accepted) { baseline = structuredClone(next); values = structuredClone(next); retry = undefined; initialID = crypto.randomUUID(); }
    },
  };
}
export function createRequestGate() {
  let epoch = 0, abort;
  return {
    next() { abort?.abort(); abort = new AbortController(); return {epoch: ++epoch, signal: abort.signal}; },
    current(token) { return token.epoch === epoch && !token.signal.aborted; },
    close() { epoch++; abort?.abort(); },
  };
}
