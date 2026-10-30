import test from 'node:test';
import assert from 'node:assert/strict';
import {createProjectionLifetime} from '../../web/src/state/projection-lifetime.mjs';
import {projectChatReplies} from '../../web/src/state/chat-replies.mjs';

test('durable omission: dropped row whose id is a source message gets suppressed', () => {
  const life = createProjectionLifetime();
  const r1 = life.update([{ viewKey: 'a', id: 'a' }, { viewKey: 'b', id: 'b' }], { sourceMessages: [{ id: 'a' }, { id: 'b' }] });
  assert.deepEqual(r1.suppressedKeys, []);
  const r2 = life.update([{ viewKey: 'b', id: 'b' }], { sourceMessages: [{ id: 'a' }, { id: 'b' }] });
  assert.ok(r2.suppressedKeys.includes('a'));
  assert.ok(!r2.ephemeralKeys.includes('a'));
});

test('ephemeral disappearance: missing ephemeral row stays in both arrays', () => {
  const life = createProjectionLifetime();
  life.update([{ viewKey: 'e', id: 'e1' }], { ephemeralKeys: ['e'] });
  const r = life.update([], { sourceMessages: [] });
  assert.ok(r.suppressedKeys.includes('e'));
  assert.ok(r.ephemeralKeys.includes('e'));
});

test('reset returns undefined then update([],{}) observes empty arrays', () => {
  const life = createProjectionLifetime();
  life.update([{ viewKey: 'a', id: 'a' }], { suppressedKeys: ['x'], ephemeralKeys: ['y'] });
  life.update([{ viewKey: 'b', id: 'b' }], {});
  assert.equal(life.reset(), undefined);
  const r = life.update([], {});
  assert.deepEqual(r.suppressedKeys, []);
  assert.deepEqual(r.ephemeralKeys, []);
});

test('projectChatReplies uses native assistant keys and completes lifecycle', () => {
  const messages = [
    { id: 'u', role: 'user', text: 'hi', request_id: 'req1' },
    { id: 'a1', role: 'assistant', text: 'hello', reply_to: 'u', status: 'complete', request_id: 'req1' },
  ];
  const snapshot = { messages, replies: [{ input_id: 'u', state: 'completed' }] };
  const out = projectChatReplies(snapshot, null);
  const a = out.find((r) => r.id === 'a1');
  assert.ok(a);
  assert.equal(a.viewKey, 'a1');
});

test("steering accepted native row uses pending logical slot and promotes durable", () => {
  const life = createProjectionLifetime();
  const pending = { id: "pending:r", viewKey: "input:r", role: "user", text: "Guide" };
  life.update([pending], { ephemeralKeys: ["input:r"] });

  const accepted = {
    id: "steer-native",
    role: "user",
    text: "Guide",
    status: "accepted",
    steering: true,
    accepted_request_id: "r",
    accepted_turn_id: "turn",
    sequence: "12",
  };

  const rows = projectChatReplies({ messages: [accepted], replies: [] });
  assert.equal(rows[0].viewKey, "input:r");

  const lifetime = life.update(rows, { sourceMessages: [accepted] });
  assert.ok(Array.isArray(lifetime.suppressedKeys));
  assert.ok(Array.isArray(lifetime.ephemeralKeys));
  assert.ok(!lifetime.suppressedKeys.includes("input:r"));
  assert.ok(!lifetime.ephemeralKeys.includes("input:r"));

  const completed = { ...accepted, status: "complete" };
  const doneRows = projectChatReplies({ messages: [completed], replies: [] });
  assert.equal(doneRows[0].viewKey, "input:r");
});

test("accepted metadata without steering retains native ids", () => {
  const life = createProjectionLifetime();
  const ordinaryUser = {
    id: "user-native",
    role: "user",
    text: "Hello",
    status: "accepted",
    accepted_request_id: "r1",
    accepted_turn_id: "turn1",
  };
  const assistant = {
    id: "assistant-native",
    role: "assistant",
    text: "Hi",
    status: "accepted",
    accepted_request_id: "r1",
    accepted_turn_id: "turn1",
  };

  const rows = projectChatReplies({ messages: [ordinaryUser, assistant], replies: [] });
  assert.equal(rows[0].viewKey, "user-native");
  assert.equal(rows[1].viewKey, "assistant-native");

  const lifetime = life.update(rows, { sourceMessages: [ordinaryUser, assistant] });
  assert.ok(Array.isArray(lifetime.suppressedKeys));
  assert.ok(Array.isArray(lifetime.ephemeralKeys));
  assert.ok(!lifetime.suppressedKeys.includes("user-native"));
  assert.ok(!lifetime.suppressedKeys.includes("assistant-native"));
  assert.ok(!lifetime.ephemeralKeys.includes("user-native"));
  assert.ok(!lifetime.ephemeralKeys.includes("assistant-native"));
});
