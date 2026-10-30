import { migrateTaskParameters } from './0011_task_parameters.mjs';

export const UI_CACHE_VERSION = 12;

export function migrateChatRecords(db, tx, oldVersion) {
  migrateTaskParameters(db, tx, oldVersion);
  if (oldVersion >= 12) return;

  const chatRecords = db.createObjectStore('chatRecords', { keyPath: ['key', 'record_id'] });
  chatRecords.createIndex('cache', 'cache_key');
  chatRecords.createIndex('target', 'key');
  chatRecords.createIndex('visible', ['key', 'visible']);
  chatRecords.createIndex('request', ['key', 'request_id'], { unique: true });
  chatRecords.createIndex('server', ['key', 'server_id']);

  const chatRecordFiles = db.createObjectStore('chatRecordFiles', { keyPath: ['target', 'record_id', 'id'] });
  chatRecordFiles.createIndex('record', ['target', 'record_id']);

  const outbox = tx.objectStore('chatOutbox');
  if (outbox.indexNames.contains('request')) outbox.deleteIndex('request');
  outbox.createIndex('request', ['target.key', 'request_id'], { unique: true });
  if (outbox.indexNames.contains('active')) outbox.deleteIndex('active');
  outbox.createIndex('active', 'active');

  const results = {};
  let pending = 4;
  const fail = () => { try { tx.abort(); } catch (_) {} };

  const done = (name) => (e) => {
    results[name] = e.target.result;
    if (--pending === 0) {
      try {
        processChatRecords(db, tx, results);
      } catch (err) {
        fail();
      }
    }
  };

  const onError = () => fail();

  const rConversations = tx.objectStore('conversations').getAll();
  const rMessages = tx.objectStore('messages').getAll();
  const rOutbox = tx.objectStore('chatOutbox').getAll();
  const rOutboxFiles = tx.objectStore('chatOutboxFiles').getAll();

  rConversations.onsuccess = done('conversations');
  rConversations.onerror = onError;
  rMessages.onsuccess = done('messages');
  rMessages.onerror = onError;
  rOutbox.onsuccess = done('chatOutbox');
  rOutbox.onerror = onError;
  rOutboxFiles.onsuccess = done('chatOutboxFiles');
  rOutboxFiles.onerror = onError;
}


function serverRecords(conversations, messages) {
  const cmap = new Map();
  for (const row of conversations) {
    try {
      const k = JSON.parse(row.key);
      if (!Array.isArray(k) || k.length !== 3 || !k.every(x => typeof x === 'string' && x)) continue;
      const p = row.projection;
      if (!p || typeof p.runtime_id !== 'string' || !p.runtime_id || typeof p.conversation_id !== 'string' || !p.conversation_id) continue;
      const o = JSON.parse(p.owner_id);
      if (!Array.isArray(o) || o.length !== 2 || typeof o[0] !== 'string' || !o[0] || typeof o[1] !== 'string' || !o[1] || o[0] !== k[1] || o[1] !== k[2]) continue;
      const target = { host: p.runtime_id, control: k[0], space: k[1], owner: k[2], conversation: p.conversation_id };
      target.key = JSON.stringify([target.host, target.control, target.space, target.owner, target.conversation]);
      cmap.set(row.key, target);
    } catch {}
  }
  const out = new Map();
  for (const inputRow of messages) {
    try {
      const target = cmap.get(inputRow.key);
      if (!target) continue;
      const m = inputRow.message;
      if (!m || typeof m.id !== 'string' || !m.id) continue;
      const requestID = m.role === 'user' ? (m.request_id || m.accepted_request_id) : null;
      const record_id = requestID ? 'input:' + requestID : 'message:' + m.id;
      const now = Date.now();
      const row = {
        schema_version: 1,
        key: target.key,
        target,
        cache_key: inputRow.key,
        record_id,
        server_id: m.id,
        message: m,
        local_owned: false,
        materialized: true,
        cancelled: false,
        visible: 0,
        revision: 1,
        created: inputRow.used || now,
        updated: now
      };
      if (requestID) row.request_id = requestID;
      out.set(JSON.stringify([target.key, record_id]), row);
    } catch {}
  }
  return out;
}

function localRecord(value, records) {
  const { target, request } = value;
  const fields = ['host', 'control', 'space', 'owner', 'conversation'];
  if (!target || typeof target !== 'object') throw Error('invalid target');
  for (const f of fields) {
    if (typeof target[f] !== 'string' || target[f].length === 0) throw Error('invalid target.' + f);
  }
  if (target.key !== JSON.stringify([target.host, target.control, target.space, target.owner, target.conversation])) throw Error('invalid target.key');
  if (!request || typeof request.request_id !== 'string' || request.request_id.length === 0) throw Error('invalid request.request_id');

  const record_id = 'input:' + request.request_id;
  const key = JSON.stringify([target.key, record_id]);
  const retry = !!request.retry_user_message_id;
  const cancelled = value.state === 'cancelled';

  let existing = null;
  if (!retry) {
    for (const row of records.values()) {
      if (row.key === target.key && row.message?.role === 'user' &&
          (row.record_id === record_id || (value.message_id && row.server_id === value.message_id))) {
        existing = row;
        break;
      }
    }
  }

  const metadata = { ...value, schema_version: 2, request_id: request.request_id, record_ref: [target.key, record_id] };
  delete metadata.request;

  if (existing && !cancelled) {
    Object.assign(metadata, { state: 'accepted', projection_pending: false, claim: null, message_id: existing.server_id });
  }

  metadata.active = metadata.state === 'cancelled' || (metadata.state === 'accepted' && metadata.projection_pending !== true) ? 0 : 1;

  const materialized = !!existing || !!(value.state === 'accepted' && value.projection_pending === false && value.message_id);

  const row = {
    ...existing,
    schema_version: 1,
    key: target.key,
    target,
    cache_key: JSON.stringify([target.control, target.space, target.owner]),
    record_id,
    request_id: request.request_id,
    request,
    local_owned: true,
    delivery_sequence: value.sequence,
    materialized,
    cancelled,
    visible: cancelled || materialized || retry ? 0 : 1,
    revision: 1,
    created: value.created_at_ms || value.created || Date.now(),
    updated: Date.now(),
    server_id: existing?.server_id || value.message_id
  };

  row.delivery = {};
  for (const p of ['state', 'problem', 'diagnostic', 'revision', 'started', 'projection_pending', 'message_id']) {
    row.delivery[p] = metadata[p];
  }

  if (existing && JSON.stringify([existing.key, existing.record_id]) !== key) {
    records.delete(JSON.stringify([existing.key, existing.record_id]));
  }
  records.set(key, row);
  return metadata;
}


function processChatRecords(db, tx, results) {
  const records = serverRecords(results.conversations, results.messages);
  const metadataByKey = new Map();
  for (const entry of results.chatOutbox) {
    const metadata = localRecord(entry, records);
    tx.objectStore('chatOutbox').put(metadata);
    metadataByKey.set(JSON.stringify([metadata.target.key, metadata.request_id]), metadata);
  }
  for (const record of records.values()) {
    tx.objectStore('chatRecords').put(record);
  }
  for (const file of results.chatOutboxFiles) {
    const metadata = metadataByKey.get(JSON.stringify([file.target, file.request]));
    if (!metadata) continue;
    if (metadata.state === 'cancelled') continue;
    if (metadata.state === 'accepted' && metadata.projection_pending === false) continue;
    const row = { ...file, record_id: 'input:' + file.request };
    delete row.request;
    tx.objectStore('chatRecordFiles').put(row);
  }
  db.deleteObjectStore('chatOutboxFiles');
}
