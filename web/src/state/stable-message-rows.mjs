const sequenceOf = (row) => {
  if (row === null || typeof row !== 'object') return null;
  if (row.record_id !== undefined && row.record_id !== null) {
    const mount = row.mount_order;
    if (typeof mount !== 'string' || !/^-?(0|[1-9][0-9]*)$/.test(mount)) {
      throw new Error('Invalid mount_order for canonical row ' + String(row.record_id));
    }
    try { return BigInt(mount); } catch { throw new Error('Invalid mount_order for canonical row ' + String(row.record_id)); }
  }
  const sequence = row.sequence;
  let value;
  if (typeof sequence === 'number') {
    if (!Number.isSafeInteger(sequence) || sequence < 0) return null;
    value = BigInt(sequence);
  } else if (typeof sequence === 'string') {
    if (!/^(0|[1-9][0-9]*)$/.test(sequence)) return null;
    try { value = BigInt(sequence); } catch { return null; }
  } else {
    return null;
  }
  return value;
};

function classifyNewDurable(previous, incoming, ephemeral) {
  const priorKeys = new Set(previous.map((row) => row.viewKey));
  const durable = previous.filter((row) => !ephemeral.has(row.viewKey));
  const positions = new Map(durable.map((row, i) => [row.viewKey, i]));
  let lastPosition = -1;
  let firstKnown = -1;
  let lastKnown = -1;
  const incomingDurable = [];
  for (let i = 0; i < incoming.length; i++) {
    const row = incoming[i];
    if (ephemeral.has(row.viewKey)) continue;
    incomingDurable.push(i);
    if (positions.has(row.viewKey)) {
      const pos = positions.get(row.viewKey);
      if (pos <= lastPosition) throw new Error('Durable rows must strictly increase');
      lastPosition = pos;
      if (firstKnown === -1) firstKnown = i;
      lastKnown = i;
    }
  }
  const incomingSequences = incomingDurable.map((index) => sequenceOf(incoming[index]));
  if (incomingSequences.every((n) => n !== null)) {
    for (let i = 1; i < incomingSequences.length; i++) {
      if (incomingSequences[i] <= incomingSequences[i - 1]) throw new Error('Incoming durable sequence out of order');
    }
  }
  const nums = durable.map((row) => sequenceOf(row));
  const numeric = nums.every((n) => n !== null);
  if (numeric) {
    for (let i = 1; i < nums.length; i++) {
      if (nums[i] <= nums[i - 1]) throw new Error('Prior durable sequence out of order');
    }
  }
  const newRows = incoming.filter((row, i) => !ephemeral.has(row.viewKey) && !priorKeys.has(row.viewKey));
  const prefix = [];
  const suffix = [];
  if (durable.length === 0) return [[], newRows];
  const min = numeric ? nums[0] : null;
  const max = numeric ? nums[nums.length - 1] : null;
  for (let i = 0; i < incoming.length; i++) {
    const row = incoming[i];
    if (ephemeral.has(row.viewKey) || priorKeys.has(row.viewKey)) continue;
    const seq = sequenceOf(row);
    if (numeric && seq !== null) {
      if (seq < min) prefix.push(row);
      else if (seq > max) suffix.push(row);
      else throw new Error('Interior sequence cannot be mounted');
    } else {
      const beforeBridge = firstKnown >= 0 && i < firstKnown && incoming[firstKnown].viewKey === durable[0].viewKey;
      const afterBridge = lastKnown >= 0 && i > lastKnown && incoming[lastKnown].viewKey === durable[durable.length - 1].viewKey;
      if (beforeBridge) prefix.push(row);
      else if (afterBridge) suffix.push(row);
      else throw new Error('History must bridge before mounting');
    }
  }
  return [prefix, suffix];
}

export function mergeConversationRows(previous, incoming, { suppressedKeys = [], ephemeralKeys = [] } = {}) {
  const suppression = new Set(suppressedKeys);
  const ephemeral = new Set(ephemeralKeys);
  const validate = (rows, label) => {
    const map = new Map();
    for (const row of rows) {
      const key = row && row.viewKey;
      if (typeof key !== 'string' || key.length === 0) throw new Error(`Invalid viewKey in ${label}`);
      if (map.has(key)) throw new Error(`Duplicate viewKey in ${label}: ${key}`);
      map.set(key, row);
    }
    return map;
  };
  const prior = validate(previous, 'previous');
  const changes = validate(incoming, 'incoming');
  const [prefix, suffix] = classifyNewDurable(previous, incoming, ephemeral);
  let result;
  if (previous.length === 0) {
    result = [...incoming];
  } else {
    result = [...prefix, ...previous.map(row => changes.get(row.viewKey) || row), ...suffix];
  }
  const present = new Set(result.map(row => row.viewKey));
  for (const [index, row] of incoming.entries()) {
    if (!ephemeral.has(row.viewKey) || prior.has(row.viewKey) || present.has(row.viewKey)) continue;
    const subsequent = incoming.slice(index + 1);
    const next = subsequent.find(r => present.has(r.viewKey));
    const pos = next ? result.findIndex(r => r.viewKey === next.viewKey) : result.length;
    result.splice(pos, 0, row);
    present.add(row.viewKey);
  }
  return result.map(row => ({ ...row, presentationSuppressed: suppression.has(row.viewKey) }));
}
