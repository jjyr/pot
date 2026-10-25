// UI-only bounded projection. No UI window is ever submitted to the agent.
import {messageBytes} from './projection-bytes.mjs';
import {activeTool} from './tool-calls.mjs';
export const HISTORY_CACHE_LIMIT = 640;
const CACHE_BYTES = 16 * 1024 * 1024;
const sequence = message => BigInt(message.sequence || 0);
const weights = new WeakMap();
function weight(message) {
  // Raw Task backfill rows mutate as events arrive; only versioned committed
  // rows can reuse an identity's encoded size across projection merges.
  if(message.version===undefined)return messageBytes(message);
  const saved=weights.get(message);if(saved?.version===message.version)return saved.bytes;
  const bytes=messageBytes(message);weights.set(message,{version:message.version,bytes});return bytes;
}
function bounded(messages, anchor) {
  let bytes = messages.reduce((sum, message) => sum + weight(message), 0);
  const pivot = BigInt(anchor || messages.at(-1)?.sequence || 0);
  while (messages.length > 1 && (messages.length > HISTORY_CACHE_LIMIT || bytes > CACHE_BYTES)) {
    const first = sequence(messages[0]), last = sequence(messages.at(-1));
    const preferred = pivot - first > last - pivot ? 0 : messages.length - 1;
    const pinned=message=>message.context_only||message.status==='streaming'||['thinking','streaming'].includes(message.phase)||activeTool(message.tool);
    let index=preferred;
    if(pinned(messages[index]))index=preferred===0?messages.findIndex(message=>!pinned(message)):messages.findLastIndex(message=>!pinned(message));
    if(index<0)throw Error('chat_cache_capacity');
    bytes -= weight(messages[index]); messages.splice(index, 1);
  }
  if(bytes>CACHE_BYTES)throw Error('chat_cache_capacity');
  return messages;
}
function combine(previous, incoming, anchor, {replace = false, page = false} = {}) {
  const entries = new Map((replace ? [] : previous?.messages || []).map(message => [message.id, message]));
  const normal = (previous?.messages || []).filter(message => !message.context_only);
  const first = sequence(normal[0] || {}), last = sequence(normal.at(-1) || {});
  const updates = new Map(previous?._updates || incoming._updates || []);
  for (let message of incoming.messages) {
    if (!page && incoming.kind === 'delta') {
      updates.delete(message.id); updates.set(message.id, message);
      while (updates.size > 256) updates.delete(updates.keys().next().value);
      // Updates to unloaded history don't grow the visible window. A reader of
      // older pages fetches the newer interval explicitly or chooses Latest.
      if (!entries.has(message.id) && !message.context_only && normal.length && (sequence(message) < first || previous.history?.has_newer && sequence(message) > last)) continue;
    }
    const update = updates.get(message.id);
    if (page && update && update.version > message.version) message = {...update,context_only:!!message.context_only};
    const old = entries.get(message.id);
    if (old && old.version > message.version) continue;
    if (old && !old.context_only && message.context_only) message = {...message, context_only: false};
    entries.set(message.id, message);
  }
  let updateBytes=[...updates.values()].reduce((sum,message)=>sum+weight(message),0);
  while(updates.size>1&&updateBytes>CACHE_BYTES){const key=updates.keys().next().value;updateBytes-=weight(updates.get(key));updates.delete(key);}
  if(updateBytes>CACHE_BYTES)throw Error('chat_cache_capacity');
  const messages = bounded([...entries.values()].sort((a, b) => sequence(a) < sequence(b) ? -1 : sequence(a) > sequence(b) ? 1 : 0), anchor);
  const rows = messages.filter(message => !message.context_only);
  const latest = [previous?._latest || '0', incoming._latest || '0', incoming.history?.after || '0', ...incoming.messages.map(message => message.sequence || '0')].reduce((a, b) => BigInt(a) > BigInt(b) ? a : b);
  const history = {...incoming.history, before: rows[0]?.sequence || '', after: rows.at(-1)?.sequence || ''};
  history.has_more = !!history.before && BigInt(history.before) > 1n;
  history.has_newer = !!history.after && BigInt(history.after) < BigInt(latest);
  return {messages, history, _latest: latest, _updates: updates};
}
export function mergeUIFrame(previous, incoming, anchor) {
  if (!['reset', 'delta'].includes(incoming.kind)) return incoming;
  if (incoming.kind === 'delta' && (!previous || String(incoming.base_cursor) !== previous.cursor)) throw Error('conversation_gap');
  const merged={...incoming, ...combine(previous, incoming, anchor, {replace: incoming.kind === 'reset'}), raw: undefined};
  // Lifecycle deltas update only their inputs. Keep other visible outcomes until
  // a reset replaces them, and evict state with the bounded message window.
  const replies=new Map((incoming.kind==='delta'?previous.replies:[]).map(reply=>[reply.input_id,reply]));
  for(const reply of incoming.replies)replies.set(reply.input_id,reply);
  const inputs=new Set(merged.messages.map(message=>message.id));
  merged.replies=[...replies.values()].filter(reply=>inputs.has(reply.input_id));
  return merged;
}
export function mergeHistoryPage(snapshot, page, anchor, {around = false} = {}) {
  if (!snapshot || page.boot_id !== snapshot.boot_id || page.conversation_id !== snapshot.conversation_id) return null;
  if(BigInt(snapshot.cursor)-BigInt(page.cursor)>256n)throw Error('history_gap');
  return {...snapshot, ...combine(snapshot, page, anchor, {replace: around, page: true})};
}
