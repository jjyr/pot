import { idbRequest } from './indexed-store.mjs';

export const recordStores = ['chatRecords', 'chatRecordFiles', 'chatOutbox'];

export function recordTarget(cacheKey, snapshot) {
  let control, space, owner, runtime_id, conversation_id;
  try {
    const parsed = JSON.parse(cacheKey);
    if (!Array.isArray(parsed) || parsed.length !== 3) return null;
    [control, space, owner] = parsed;
    if (typeof control !== 'string' || typeof space !== 'string' || typeof owner !== 'string') return null;
  } catch {
    return null;
  }
  const ownerId = snapshot && snapshot.owner_id;
  let ownerSpace, ownerOwner;
  try {
    const parsed = JSON.parse(ownerId);
    if (!Array.isArray(parsed) || parsed.length !== 2) return null;
    [ownerSpace, ownerOwner] = parsed;
  } catch {
    return null;
  }
  if (ownerSpace !== space || ownerOwner !== owner) return null;
  runtime_id = snapshot.runtime_id;
  conversation_id = snapshot.conversation_id;
  if (typeof runtime_id !== 'string' || !runtime_id) return null;
  if (typeof conversation_id !== 'string' || !conversation_id) return null;
  const host = runtime_id;
  const conversation = conversation_id;
  const key = JSON.stringify([host, control, space, owner, conversation]);
  return { host, control, space, owner, conversation, key };
}

export function recordCacheKey(target) {
  return JSON.stringify([target.control, target.space, target.owner]);
}

export function recordID(message) {
  if (message && message.role === 'user') {
    const rid = message.request_id || message.accepted_request_id;
    if (rid) return 'input:' + rid;
  }
  return 'message:' + (message && message.id);
}

export async function hydrateDelivery(tx, value) {
  if (value == null) return null;
  const ref = value.record_ref;
  if (!Array.isArray(ref) || ref.length !== 2) {
    throw new Error('invalid outbox record_ref');
  }
  const [key, recordId] = ref;
  const record = await idbRequest(tx.objectStore('chatRecords').get([key, recordId]));
  if (!record) throw new Error('outbox record missing');
  if (record.key !== key || record.record_id !== recordId) {
    throw new Error('outbox record identity mismatch');
  }
  if (record.request_id !== value.request_id) {
    throw new Error('outbox record request mismatch');
  }
  if (!record.target || !value.target || record.target.key !== value.target.key) {
    throw new Error('outbox record target key mismatch');
  }
  if (record.target.host !== value.target.host || record.target.control !== value.target.control || record.target.space !== value.target.space || record.target.owner !== value.target.owner || record.target.conversation !== value.target.conversation) {
    throw new Error('outbox record target mismatch');
  }
  return { ...value, request: record.request, record_id: record.record_id };
}

export async function putLocalRecord(tx, value, sequence) {
  const { target, request, created } = value;
  const recordId = 'input:' + request.request_id;
  const now = Date.now();
  const record = {
    schema_version: 1,
    key: target.key,
    record_id: recordId,
    target,
    cache_key: recordCacheKey(target),
    request_id: request.request_id,
    request,
    local_owned: true,
    materialized: false,
    cancelled: false,
    visible: request.retry_user_message_id ? 0 : 1,
    revision: 1,
    delivery_sequence: sequence,
    created: value.created_at_ms || created || now,
    updated: now,
  };
  await idbRequest(tx.objectStore('chatRecords').add(record));
  return record;
}

export async function syncDeliveryRecord(tx, value) {
  const ref = value.record_ref;
  const [key, recordId] = ref;
  const store = tx.objectStore('chatRecords');
  const record = await idbRequest(store.get([key, recordId]));
  if (!record) throw new Error('delivery record missing');
  if (record.key !== key || record.record_id !== recordId) {
    throw new Error('delivery record identity mismatch');
  }
  if (value.request_id !== record.request_id) {
    throw new Error('delivery record request identity mismatch');
  }
  if (!record.target || !value.target || record.target.key !== value.target.key) {
    throw new Error('delivery record target key mismatch');
  }
  if (record.target.host !== value.target.host || record.target.control !== value.target.control || record.target.space !== value.target.space || record.target.owner !== value.target.owner || record.target.conversation !== value.target.conversation) {
    throw new Error('delivery record target mismatch');
  }
  const prevRequest = record.request;
  const nextRequest = value.request !== undefined ? value.request : prevRequest;
  if (nextRequest && nextRequest.request_id !== record.request_id) {
    throw new Error('delivery record request identity mismatch');
  }
  let nextServerId = record.server_id;
  if (value.message_id) nextServerId = value.message_id;
  const nextCancelled = value.state === 'cancelled' ? true : record.cancelled;
  const nextMaterialized = record.materialized;
  const nextVisible = !nextCancelled && !nextMaterialized && !(nextRequest && nextRequest.retry_user_message_id) ? 1 : 0;

  const prevSummary = record.delivery || {};
  const nextSummary = {
    state: value.state,
    problem: value.problem,
    diagnostic: value.diagnostic,
    revision: value.revision,
    started: value.started,
    projection_pending: value.projection_pending,
    message_id: value.message_id,
  };

  const contentChanged =
    JSON.stringify(prevRequest) !== JSON.stringify(nextRequest) ||
    nextServerId !== record.server_id ||
    nextCancelled !== record.cancelled ||
    nextVisible !== record.visible ||
    JSON.stringify(prevSummary) !== JSON.stringify(nextSummary);

  const next = {
    ...record,
    request: nextRequest,
    server_id: nextServerId,
    cancelled: nextCancelled,
    visible: nextVisible,
    delivery: nextSummary,
    updated: Date.now(),
  };
  if (!contentChanged) return record;
  next.revision = (record.revision || 0) + 1;

  await idbRequest(store.put(next));
  return next;
}

export async function clearRecordFiles(tx, record) {
  if (!record || !record.local_owned) return;
  const store = tx.objectStore('chatRecordFiles');
  const keys = await idbRequest(store.index('record').getAllKeys([record.key, record.record_id]));
  for (const k of keys) store.delete(k);
}

export async function upsertServerRecords(tx, cacheKey, identity, messages) {
  const target = recordTarget(cacheKey, identity);
  if (!target) return;
  const records = tx.objectStore('chatRecords');
  const outbox = tx.objectStore('chatOutbox');

  for (const message of messages) {
    if (!message || !message.id) continue;
    const isUser = message.role === 'user';
    const userReq = isUser ? (message.request_id || message.accepted_request_id) : null;
    let record = null;

    if (userReq) {
      const rows = await idbRequest(records.index('request').getAll([target.key, userReq]));
      for (const row of rows) {
        if (!row.request || !row.request.retry_user_message_id) { record = row; break; }
      }
    }
    if (!record) {
      const rows = await idbRequest(records.index('server').getAll([target.key, message.id]));
      for (const row of rows) {
        if (!row.request || !row.request.retry_user_message_id) { record = row; break; }
      }
    }
    if (!record) {
      const rid = recordID(message);
      record = await idbRequest(records.get([target.key, rid]));
      if (record && record.request && record.request.retry_user_message_id) record = null;
    }

    const now = Date.now();
    if (!record) {
      const rid = recordID(message);
      const fresh = {
        schema_version: 1,
        key: target.key,
        record_id: rid,
        target,
        cache_key: recordCacheKey(target),
        server_id: message.id,
        message,
        local_owned: false,
        materialized: true,
        cancelled: false,
        visible: 0,
        revision: 1,
        created: now,
        updated: now,
      };
      if (isUser && userReq) fresh.request_id = userReq;
      await idbRequest(records.add(fresh));
      continue;
    }



    const prevJSON = JSON.stringify(record.message);
    record.message = message;
    record.server_id = message.id;
    record.materialized = true;
    record.visible = 0;
    record.updated = now;
    if (JSON.stringify(message) !== prevJSON) record.revision = (record.revision || 0) + 1;

    const delivery = record.delivery;
    if (delivery) {
      const outboxKey = record.delivery_sequence;
      if (outboxKey !== undefined && outboxKey !== null) {
        const existing = await idbRequest(outbox.get(outboxKey));
        if (existing) {
          const ref = existing.record_ref;
          const okRef = Array.isArray(ref) && ref.length === 2 && ref[0] === record.key && ref[1] === record.record_id;
          const okReq = existing.request_id === record.request_id && existing.target?.key === record.key && ['host','control','space','owner','conversation'].every(field => existing.target?.[field] === record.target?.[field]);
          if (okRef && okReq) {
            const raw = { ...existing };
            if (raw.state !== 'cancelled') {
              let changed = false;
              if (raw.state !== 'accepted') { raw.state = 'accepted'; changed = true; }
              if (raw.message_id !== message.id) { raw.message_id = message.id; changed = true; }
              if (raw.claim != null) { raw.claim = null; changed = true; }
              if (raw.projection_pending !== false) { raw.projection_pending = false; changed = true; }
              if (raw.active !== 0) { raw.active = 0; changed = true; }
              if (changed) {
                raw.revision = (raw.revision || 0) + 1;
                await idbRequest(outbox.put(raw));
              }
            }
            record.delivery = {
              state: raw.state,
              problem: raw.problem,
              diagnostic: raw.diagnostic,
              revision: raw.revision,
              started: raw.started,
              projection_pending: raw.projection_pending,
              message_id: raw.message_id,
            };
          }
        }
      }
    }

    await idbRequest(records.put(record));
    const dstate = record.delivery && record.delivery.state;
    const ppending = record.delivery && record.delivery.projection_pending;
    if (dstate === 'cancelled' || (dstate === 'accepted' && ppending === false)) {
      await clearRecordFiles(tx, record);
    }
  }
}

export async function removeServerRecord(tx, cacheKey, identity, message) {
  const target = recordTarget(cacheKey, identity);
  if (!target || !message || !message.id) return;
  const records = tx.objectStore('chatRecords');
  const rows = await idbRequest(records.index('server').getAll([target.key, message.id]));
  for (const row of rows) {
    if (!row.local_owned) records.delete([row.key, row.record_id]);
  }
}

export async function eraseServerRecords(tx, cacheKey) {
  let cacheKeyJSON;
  try { cacheKeyJSON = JSON.stringify(JSON.parse(cacheKey)); } catch { return; }
  const records = tx.objectStore('chatRecords');
  const range = IDBKeyRange.only(cacheKeyJSON);
  const cursorReq = records.index('cache').openCursor(range);
  await new Promise((resolve, reject) => {
    cursorReq.onsuccess = () => {
      const cursor = cursorReq.result;
      if (!cursor) return resolve();
      if (!cursor.value.local_owned) cursor.delete();
      cursor.continue();
    };
    cursorReq.onerror = () => reject(cursorReq.error);
  });
}

function serverSequence(record) {
  const v = record.message && record.message.sequence;
  if (typeof v === 'bigint') return v >= 0n ? v.toString() : null;
  if (typeof v === 'string') {
    if (!/^[0-9]+$/.test(v)) return null;
    try {
      const b = BigInt(v);
      return b >= 0n ? b.toString() : null;
    } catch {
      return null;
    }
  }
  if (typeof v === 'number') return Number.isSafeInteger(v) && v >= 0 ? v.toString() : null;
  return null;
}

function compareRecords(a, b) {
  const sa = serverSequence(a);
  const sb = serverSequence(b);
  if (sa !== null && sb !== null) {
    const ba = BigInt(sa);
    const bb = BigInt(sb);
    if (ba < bb) return -1;
    if (ba > bb) return 1;
    return 0;
  }
  if (sa !== null) return -1;
  if (sb !== null) return 1;
  const da = a.delivery_sequence;
  const db = b.delivery_sequence;
  if (typeof da === 'number' && typeof db === 'number') {
    if (da < db) return -1;
    if (da > db) return 1;
    return 0;
  }
  if (typeof da === 'number') return -1;
  if (typeof db === 'number') return 1;
  const ca = a.created || 0;
  const cb = b.created || 0;
  if (ca < cb) return -1;
  if (ca > cb) return 1;
  return 0;
}

export async function readRecordRows(tx, cacheKey, snapshot, options = {}) {
  const { retainedRecordIDs = [], validate = () => {} } = options;
  const target = recordTarget(cacheKey, snapshot);
  if (!target) return snapshot;
  validate(target);

  const records = tx.objectStore('chatRecords');
  const retainedSet = new Set(retainedRecordIDs);
  const found = new Map();

  async function lookupMessage(message) {
    if (!message) return null;
    if (message.role === 'user') {
      const reqId = message.request_id || message.accepted_request_id;
      if (reqId) {
        const rows = await idbRequest(records.index('request').getAll([target.key, reqId]));
        for (const row of rows) {
          if (!row.request || !row.request.retry_user_message_id) return row;
        }
      }
    }
    if (message.id) {
      const rows = await idbRequest(records.index('server').getAll([target.key, message.id]));
      for (const row of rows) {
        if (!row.request || !row.request.retry_user_message_id) return row;
      }
    }
    return null;
  }

  const contextOnlyByID = new Map();
  for (const message of snapshot.messages || []) {
    const rid = retainedSet.has(recordID(message)) ? recordID(message) : null;
    let row = rid ? await idbRequest(records.get([target.key, rid])) : null;
    if (!row) row = await lookupMessage(message);
    if (!row) {
      row = { record_id: recordID(message), message, materialized: true, revision: 0 };
    }
    if (row) {
      found.set(row.record_id, row);
      contextOnlyByID.set(row.record_id, !!message.context_only);
    }
  }

  for (const rid of retainedRecordIDs) {
    const row = await idbRequest(records.get([target.key, rid]));
    if (row) found.set(row.record_id, row);
  }

  const visibleRows = await idbRequest(records.index('visible').getAll([target.key, 1]));
  for (const row of visibleRows) {
    if (row.cancelled) continue;
    if (row.request && row.request.retry_user_message_id) continue;
    found.set(row.record_id, row);
  }

  const all = [];
  const suppressed = [];
  for (const row of found.values()) {
    if (row.request && row.request.retry_user_message_id) continue;
    if (row.cancelled) {
      if (retainedSet.has(row.record_id)) suppressed.push(row.record_id);
      continue;
    }
    if (row.materialized && !row.message) continue;
    all.push(row);
  }

  const newRows = [];
  for (const row of all) {
    if (!retainedSet.has(row.record_id)) newRows.push(row);
  }
  newRows.sort(compareRecords);

  let minSeq = null;
  for (const id of retainedRecordIDs) {
    const row = found.get(id);
    if (!row) continue;
    const seq = serverSequence(row);
    if (seq === null) continue;
    if (minSeq === null || BigInt(seq) < BigInt(minSeq)) minSeq = seq;
  }
  const prefixNew = [];
  const suffixNew = [];
  for (const row of newRows) {
    const seq = serverSequence(row);
    if (seq !== null && minSeq !== null && BigInt(seq) < BigInt(minSeq)) prefixNew.push(row);
    else suffixNew.push(row);
  }
  const ordered = [];
  for (const id of retainedRecordIDs) {
    const r = all.find(row => row.record_id === id);
    if (r && !r.cancelled && !ordered.includes(r)) ordered.push(r);
  }
  const finalRows = [];
  const seen = new Set();
  for (const row of [...prefixNew, ...ordered, ...suffixNew]) {
    if (row.cancelled) continue;
    if (seen.has(row.record_id)) continue;
    seen.add(row.record_id);
    finalRows.push(row);
  }

  const outbox = tx.objectStore('chatOutbox');
  const messages = [];
  for (const row of finalRows) {
    let dto;
    if (row.materialized) {
      dto = { ...row.message };
    } else if (row.request) {
      const req = row.request || {};
      dto = {
        id: 'local:' + row.record_id,
        role: 'user',
        text: req.text,
        attachments: req.attachments,
        references: req.references,
        status: 'accepted',
        request_id: row.request_id,
        created_at_ms: row.created,
      };
      if (req.user_message_id) {
        dto.steering = true;
        dto.accepted_request_id = req.request_id;
        dto.accepted_turn_id = req.user_message_id;
      }
    } else if (row.message) {
      dto = { ...row.message };
    } else {
      continue;
    }
    if (contextOnlyByID.has(row.record_id)) {
      dto.context_only = contextOnlyByID.get(row.record_id);
    }
    dto.record_id = row.record_id;
    dto.viewKey = row.record_id;
    dto.record_revision = row.revision || 0;
    dto.local_only = !row.materialized;
    dto.delivery = null;
    if (row.delivery_sequence !== undefined && row.delivery_sequence !== null) {
      const value = await idbRequest(outbox.get(row.delivery_sequence));
      if (value) {
        const hydrated = await hydrateDelivery(tx, value);
        if (hydrated) {
          dto.delivery = hydrated;
        }
      }
    }
    messages.push(dto);
  }

  return { ...snapshot, messages, suppressedRecordIDs: suppressed };
}
