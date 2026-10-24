// Display identity only. Catalogue IDs and short labels must never route requests.
export const addressKey = address => address?.node_id && address?.kind && address?.agent_id
  ? JSON.stringify([address.node_id, address.kind, address.agent_id]) : null;

// An explicit address is authoritative even when a linked catalogue has a local hash ID.
export function taskAddress(task) {
  const address = task?.address;
  return address?.kind === 'task' && addressKey(address) ? address : null;
}
export function assistantAddress(assistant) {
  if (!assistant) return null;
  const address = assistant.origin
    ? {node_id: assistant.origin.node_id, kind: 'assistant', agent_id: assistant.origin.assistant_id}
    : {node_id: assistant.control_id, kind: 'assistant', agent_id: assistant.id};
  return addressKey(address) ? address : null;
}
export function taskColorIndex(value) {
  const address = value?.kind === 'task' ? value : taskAddress(value);
  if (!addressKey(address) || address?.kind !== 'task' || typeof TextEncoder !== 'function') return -1;
  let hash = 2166136261;
  for (const byte of new TextEncoder().encode(addressKey(address))) hash = Math.imul(hash ^ byte, 16777619) >>> 0;
  return hash % 6;
}
// Keep one registry for the UI session. Discovering a collision only lengthens labels.
export function createTaskIdentityRegistry() {
  const entries = new Map(), records = new Map();
  const register = values => {
    for (const value of values) {
      const address = value?.kind === 'task' ? value : taskAddress(value), key = addressKey(address);
      if (key && address.kind === 'task' && !entries.has(key)) entries.set(key, {id: address.agent_id, node: address.node_id, length: 4, sourceLength: 0});
      if (key && value?.id && taskAddress(value)) {
        records.delete(key); records.set(key, {id: value.id, address: {...address}, title: value.title || ''});
        if (records.size > 512) records.delete(records.keys().next().value);
      }
    }
    const all = [...entries.values()];
    for (let i = 0; i < all.length; i++) for (let j = i + 1; j < all.length; j++) {
      const a = all[i], b = all[j];
      // Equal full Task IDs require the node in the shared display label.
      if (a.id === b.id) {
        let length = 4;
        while (a.node.slice(0, length) === b.node.slice(0, length)) length++;
        a.sourceLength = Math.max(a.sourceLength, length);
        b.sourceLength = Math.max(b.sourceLength, length);
        continue;
      }
      let length = 4;
      while (a.id.slice(0, length) === b.id.slice(0, length)) length++;
      if (length > 4) { a.length = Math.max(a.length, length); b.length = Math.max(b.length, length); }
    }
  };
  return {register, resolve(address) { return records.get(addressKey(address)); }, short(value) {
    const address = value?.kind === 'task' ? value : taskAddress(value);
    register([value]);
    const entry = entries.get(addressKey(address));
    return entry ? '#' + entry.id.slice(0, entry.length) + (entry.sourceLength ? '@' + entry.node.slice(0, entry.sourceLength) : '') : '';
  }};
}
export const taskIdentities = createTaskIdentityRegistry();
