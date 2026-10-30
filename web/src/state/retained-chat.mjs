export function retainCommittedChat(previous, incoming, {replace = false, direction = 'stream'} = {}) {
  const identity = value => [value.runtime_id, value.owner_id, value.conversation_id].join('\u0000');
  if (!previous || replace || identity(previous) !== identity(incoming)) {
    return {...incoming, _retainedBoots: new Map(incoming.messages.map(m => [m.id, incoming.boot_id]))};
  }
  const boots = new Map(previous._retainedBoots || previous.messages.map(m => [m.id, previous.boot_id]));
  const seq = message => BigInt(message.sequence || 0);
  const oldNormals = previous.messages.filter(message => !message.context_only).sort((a, b) => seq(a) < seq(b) ? -1 : seq(a) > seq(b) ? 1 : 0);
  const first = oldNormals.length ? seq(oldNormals[0]) : null;
  const last = oldNormals.length ? seq(oldNormals.at(-1)) : null;
  const entries = new Map(previous.messages.map(message => [message.id, message]));
  const candidates = [];
  const minimum = incoming.messages.filter(message => !message.context_only).reduce((low, message) => low === null || seq(message) < low ? seq(message) : low, null);
  const maximum = incoming.messages.filter(message => !message.context_only).reduce((high, message) => high === null || seq(message) > high ? seq(message) : high, null);
  const streamOk = last !== null && minimum !== null && maximum !== null && minimum <= last + 1n && maximum >= last && (!previous.history?.has_newer || incoming.kind === 'reset');
  const admitNormal = message => {
    if (last === null) return true;
    if (direction === 'before') return seq(message) < first;
    if (direction === 'after') return seq(message) > last;
    return streamOk && seq(message) > last;
  };
  for (const message of incoming.messages) {
    const old = entries.get(message.id);
    if (!old) {
      if (message.context_only) { entries.set(message.id, message); boots.set(message.id, incoming.boot_id); }
      else if (admitNormal(message)) { entries.set(message.id, message); boots.set(message.id, incoming.boot_id); }
      continue;
    }
    if (boots.get(old.id) === incoming.boot_id && old.version > message.version) {
      if (!old.context_only) continue;
      if (message.context_only) continue;
      if (admitNormal({...old, context_only: false})) {
        entries.set(old.id, {...old, context_only: false});
        boots.set(old.id, incoming.boot_id);
      }
      continue;
    }
    if (!old.context_only) {
      entries.set(old.id, {...message, sequence: old.sequence, context_only: false});
      boots.set(old.id, incoming.boot_id);
    } else if (!message.context_only) {
      const promoted = {...message, sequence: old.sequence};
      if (admitNormal(promoted)) {
        entries.set(old.id, promoted);
        boots.set(old.id, incoming.boot_id);
      }
    } else {
      entries.set(old.id, message);
      boots.set(old.id, incoming.boot_id);
    }
  }
  const messages = [...entries.values()].sort((a, b) => seq(a) < seq(b) ? -1 : seq(a) > seq(b) ? 1 : 0);
  const normals = messages.filter(message => !message.context_only);
  const before = String(normals[0]?.sequence || '');
  const after = String(normals.at(-1)?.sequence || '');
  let latest = BigInt(previous._latest || 0);
  for (const source of [incoming._latest, incoming.history?.after]) if (source && BigInt(source) > latest) latest = BigInt(source);
  for (const message of incoming.messages) if (message.sequence && BigInt(message.sequence) > latest) latest = BigInt(message.sequence);
  for (const message of normals) if (message.sequence && BigInt(message.sequence) > latest) latest = BigInt(message.sequence);
  const ids = new Set(normals.map(message => message.id));
  const replies = new Map((previous.replies || []).map(reply => [reply.input_id, reply]));
  for (const reply of incoming.replies || []) replies.set(reply.input_id, reply);
  candidates.push(...replies.values());
  return {
    ...incoming,
    messages,
    history: {...incoming.history, before, after, has_more: !!before && BigInt(before) > 1n, has_newer: !!after && BigInt(after) < latest},
    _latest: String(latest),
    _retainedBoots: boots,
    replies: candidates.filter(reply => ids.has(reply.input_id)),
  };
}
