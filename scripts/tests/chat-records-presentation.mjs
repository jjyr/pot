import assert from 'node:assert/strict';
import { mergeConversationRows } from '../../web/src/state/stable-message-rows.mjs';
import { createConversationState, acceptSnapshot, ownerKey } from '../../web/src/state/conversation.mjs';
import { projectChatReplies } from '../../web/src/state/chat-replies.mjs';

// Case 1: merge previous committed base, pending local-only, concurrent incoming server
{
  const base = { viewKey: 'message:base', record_id: 'base', mount_order: '0', sequence: 1 };
  const pending = { viewKey: 'input:req', request_id: 'req', local_only: true, mount_order: '1', record_id: 'input:req', id: 'local:input:req' };
  const incomingRow = { viewKey: 'message:server', record_id: 'server', mount_order: '2', sequence: 3 };
  const previous = [base, pending, incomingRow];
  const ack = { viewKey: 'input:req', request_id: 'req', record_id: 'input:req', id: 'canonical', sequence: 999, mount_order: '1', delivery: { revision: 2 }, record_revision: 2, local_only: false };
  const incoming = [base, ack, incomingRow];
  const result = mergeConversationRows(previous, incoming);
  const durableKeys = result.filter(r => r.record_id !== undefined).map(r => r.viewKey);
  assert.deepEqual(durableKeys, ['message:base', 'input:req', 'message:server']);
  const canonical = result.find(r => r.viewKey === 'input:req' && r.record_id !== undefined);
  assert.ok(canonical);
  assert.equal(canonical.id, 'canonical');
}

// Case 2: prepend seq 0 mount_order -1 and append seq 4 mount_order 3 accepted boundary
{
  const base = { viewKey: 'message:base', record_id: 'base', mount_order: '0', sequence: 1 };
  const tail = { viewKey: 'message:tail', record_id: 'tail', mount_order: '2', sequence: 3 };
  const previous = [base, tail];
  const oldRow = { viewKey: 'message:old', record_id: 'old', mount_order: '-1', sequence: 0 };
  const newRow = { viewKey: 'message:new', record_id: 'new', mount_order: '3', sequence: 4 };
  const incoming = [oldRow, base, tail, newRow];
  const result = mergeConversationRows(previous, incoming);
  const keys = result.map(r => r.viewKey);
  assert.deepEqual(keys, ['message:old', 'message:base', 'message:tail', 'message:new']);
}

// Case 3: acceptSnapshot fresh state, then same cursor with updated delivery
{
  const state = createConversationState();
  const epoch = 0;
  const baseSnapshot = {
    runtime_id: 'host',
    boot_id: 'boot',
    owner_id: JSON.stringify(['s', 'o']),
    conversation_id: 'chat',
    cursor: '1',
    running: false,
    turn_id: null,
    replies: [],
    messages: [
      { id: 'input:p1', record_id: 'input:p1', viewKey: 'input:p1', mount_order: '0', local_only: true, role: 'user', text: 'hi', status: 'pending', version: 1, delivery: { state: 'queued', revision: 1 }, record_revision: 1 }
    ]
  };
  assert.equal(acceptSnapshot(state, baseSnapshot, epoch, { committed: true }), true);
  const updatedSnapshot = {
    ...baseSnapshot,
    messages: [
      { id: 'input:p1', record_id: 'input:p1', viewKey: 'input:p1', mount_order: '0', local_only: true, role: 'user', text: 'hi', status: 'accepted', version: 1, delivery: { state: 'accepted', revision: 2 }, record_revision: 2 }
    ]
  };
  assert.equal(acceptSnapshot(state, updatedSnapshot, epoch, { committed: true }), true);
  assert.equal(state.snapshot.messages[0].status, 'accepted');
  assert.deepEqual(state.snapshot.messages[0].delivery, { state: 'accepted', revision: 2 });
}

// Case 4: send retention on local snapshot and deletion on authoritative materialized
{
  const state = createConversationState();
  const snapshot = {
    runtime_id: 'host',
    boot_id: 'boot',
    owner_id: JSON.stringify(['s', 'o']),
    conversation_id: 'chat',
    cursor: '1',
    running: false,
    turn_id: null,
    replies: [],
    messages: []
  };
  assert.equal(acceptSnapshot(state, snapshot, state.epoch, { committed: true }), true);
  const key = ownerKey(snapshot);
  state.sends.set(key, { request: { request_id: 'req' } });
  const localSnapshot = {
    ...snapshot,
    cursor: '2',
    messages: [
      { id: 'local1', role: 'user', text: 'hi', status: 'pending', request_id: 'req', local_only: true }
    ]
  };
  assert.equal(acceptSnapshot(state, localSnapshot, state.epoch, { committed: true }), true);
  assert.ok(state.sends.has(key), 'local-only snapshot must not delete send');

  const authSnapshot = {
    ...localSnapshot,
    cursor: '3',
    messages: [
      { id: 'auth1', role: 'user', text: 'hi', status: 'accepted', request_id: 'req' }
    ]
  };
  assert.equal(acceptSnapshot(state, authSnapshot, state.epoch, { committed: true }), true);
  assert.equal(state.sends.has(key), false, 'authoritative materialized must delete send');
}

// Case 5: projectChatReplies infers reply_to for assistant when omitted
{
  const messages = [
    { id: 'u1', role: 'user', text: 'hi', status: 'accepted', request_id: 'req' },
    { id: 'local', role: 'user', text: 'hi', status: 'pending', request_id: 'req', local_only: true },
    { id: 'assistant1', role: 'assistant', status: 'streaming', text: 'streaming' }
  ];
  const projected = projectChatReplies({ messages, replies: [], running: false }, null);
  const assistant = projected.find(r => r.id === 'assistant1');
  assert.ok(assistant);
  assert.equal(assistant.inputID, 'u1');
  assert.notEqual(assistant.inputID, 'local');
}

console.log('PASS');
