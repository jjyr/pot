// Display identity only. Catalogue IDs and short labels must never route requests.
export const addressKey = address => [address?.node_id,address?.kind,address?.agent_id].every(value => typeof value === 'string' && value.length > 0)
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
// Display labels deliberately allow equal prefixes; full addresses own identity/routing.
export function taskShortID(value) {
  const address=value?.kind==='task'?value:taskAddress(value);
  return address?.kind==='task'&&addressKey(address)?'#'+address.agent_id.slice(0,4):'';
}

// Keep confirmed catalogue IDs, scope and owner independently of the short label.
export function createTaskIdentityRegistry() {
  const records = new Map();
  const register = values => {
    for (const value of values) {
      const address = taskAddress(value), key = addressKey(address);
      if (key && value.id) {
        const previous=records.get(key),title=value.title||'',space=value._space_id??previous?._space_id,owner=value.assistant_id??previous?.assistant_id;
        records.delete(key); records.set(key, previous?.id===value.id&&previous.title===title&&previous._space_id===space&&previous.assistant_id===owner?previous:{id: value.id, address: {...address}, title, _space_id:space, assistant_id:owner});
        if (records.size > 512) records.delete(records.keys().next().value);
      }
    }
  };
  return {register, snapshot(){return structuredClone([...records.values()].sort((a,b)=>addressKey(a.address).localeCompare(addressKey(b.address))));}, resolve(address) { return records.get(addressKey(address)); }, short(value) {
    register([value]);return taskShortID(value);
  }};
}
export const taskIdentities = createTaskIdentityRegistry();
