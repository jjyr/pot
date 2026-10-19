import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createMCPClient} from './mcp-client.mjs';

test('negotiates once, sends initialized notification and uses negotiated version', async () => {
  const calls = [];
  const client = createMCPClient(async (body, version) => {
    calls.push({body, version});
    if (body.method === 'notifications/initialized') return;
    const result = body.method === 'initialize' ? {protocolVersion: '2025-06-18', capabilities: {tools: {}}} : body.method === 'tools/list' ? {tools: [{name: 'codex_status'}]} : {content: [{type: 'text', text: '{"status":"queued"}'}], structuredContent: {status: 'queued'}};
    return {jsonrpc: '2.0', id: body.id, result};
  });
  const [list, result] = await Promise.all([client.listTools(), client.callTool('codex_status', {task_id: 'task'})]);
  assert.equal(list.tools[0].name, 'codex_status');
  assert.deepEqual(result, {status: 'queued'});
  assert.equal(calls.filter(call => call.body.method === 'initialize').length, 1);
  assert.equal(calls[1].body.method, 'notifications/initialized');
  assert.equal(calls[1].body.id, undefined);
  assert(calls.slice(1).every(call => call.version === '2025-06-18'));
  assert.deepEqual(calls.at(-1).body.params, {name: 'codex_status', arguments: {task_id: 'task'}});
});

test('surfaces protocol and tool errors and refuses mismatched response IDs', async () => {
  for (const [response, message] of [
    [{error: {code: -32601}}, /mcp_protocol_error_-32601/],
    [{id: 999, result: {}}, /invalid_mcp_response/],
    [{result: {isError: true, content: [{type: 'text', text: '{"error":"not_found"}'}]}}, /not_found/],
    [{result: {content: [{type: 'text', text: 'invalid json'}]}}, /invalid_mcp_tool_result/],
  ]) {
    const client = createMCPClient(async body => {
      if (body.method === 'initialize') return {jsonrpc: '2.0', id: body.id, result: {protocolVersion: '2025-06-18', capabilities: {tools: {}}}};
      if (body.method === 'notifications/initialized') return;
      return {jsonrpc: '2.0', id: body.id, ...response};
    });
    await assert.rejects(client.callTool('codex_status', {task_id: 'task'}), message);
  }
});

test('failed tool calls are not retried, and text results remain usable', async () => {
  let calls = 0;
  const client = createMCPClient(async body => {
    if (body.method === 'initialize') return {jsonrpc: '2.0', id: body.id, result: {protocolVersion: '2025-06-18', capabilities: {tools: {}}}};
    if (body.method === 'notifications/initialized') return;
    if (++calls === 1) throw Error('connection_lost');
    return {jsonrpc: '2.0', id: body.id, result: {content: [{type: 'text', text: '{"id":"original"}'}]}};
  });
  await assert.rejects(client.callTool('codex_submit', {request_id: 'same'}), /connection_lost/);
  assert.equal(calls, 1);
  assert.deepEqual(await client.callTool('codex_submit', {request_id: 'same'}), {id: 'original'});
});
