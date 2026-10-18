// State is scoped by runtime, Control/assistant owner and conversation. Boot IDs
// fence subscriptions; cursors are decimal strings, never JavaScript numbers.
export function ownerKey(snapshot) {
  return JSON.stringify([snapshot.runtime_id, snapshot.owner_id, snapshot.conversation_id]);
}

export function validSnapshot(value) {
  return value && ['runtime_id', 'boot_id', 'owner_id', 'conversation_id'].every(key => typeof value[key] === 'string' && value[key].length > 0)
    && typeof value.cursor === 'string' && /^(0|[1-9]\d*)$/.test(value.cursor)
    && typeof value.running === 'boolean'
    && (value.running ? typeof value.turn_id === 'string' && value.turn_id.length > 0 : value.turn_id === null)
    && Array.isArray(value.messages)
    && value.messages.every(message => message && typeof message.id === 'string'
      && ['user', 'assistant', 'system', 'tool'].includes(message.role)
      && typeof message.text === 'string' && typeof message.status === 'string'
      && (message.error===undefined||typeof message.error==='string')
      && (message.reply_to===undefined||typeof message.reply_to==='string'));
}

export function createConversationState() {
  return {epoch: 0, snapshot: null, drafts: new Map(), pending: new Map(), retries: new Map(), sends: new Map()};
}

export function beginSubscription(state) {
  state.epoch++;
  for (const [key, pending] of state.pending) state.retries.set(key, pending.request);
  for (const send of state.sends.values())if(send.phase==='sending')send.phase='uncertain';
  state.pending.clear();
  state.snapshot = null;
  return state.epoch;
}

export function acceptSnapshot(state, snapshot, epoch) {
  if (epoch !== state.epoch || !validSnapshot(snapshot)) return false;
  const previous = state.snapshot;
  // A different identity/boot is established only by a fresh subscription. Late
  // events may not silently navigate the user into another owner's conversation.
  if (previous && (ownerKey(previous) !== ownerKey(snapshot) || previous.boot_id !== snapshot.boot_id)) return false;
  if (previous && BigInt(snapshot.cursor) <= BigInt(previous.cursor)) return false;
  state.snapshot = snapshot;
  const key=ownerKey(snapshot),send=state.sends.get(key);
  if(send&&snapshot.messages.some(message=>message.request_id===send.request.request_id))state.sends.delete(key);
  return true;
}

export function draft(state, key = state.snapshot && ownerKey(state.snapshot)) {
  return state.drafts.get(key) || '';
}

export function setDraft(state, text, key = state.snapshot && ownerKey(state.snapshot)) {
  if (key) state.drafts.set(key, text);
}

export function beginSend(state, requestID, {allowRunning=false,attachments=[],retryUserMessageID}={}) {
  if (!state.snapshot || (state.snapshot.running&&!allowRunning)) return null;
  const key = ownerKey(state.snapshot);
  const text = draft(state, key);
  if ((!text.trim() && !attachments.length) || state.pending.has(key)) return null;
  const retry = state.retries.get(key);
  const request = retry?.text === text && JSON.stringify(retry.attachments||[])===JSON.stringify(attachments) && retry.boot_id === state.snapshot.boot_id ? retry
    : {request_id: requestID, boot_id: state.snapshot.boot_id, conversation_id: state.snapshot.conversation_id, text,...(attachments.length?{attachments}:{}),...(retryUserMessageID?{retry_user_message_id:retryUserMessageID}:{}),...(state.snapshot.running?{user_message_id:state.snapshot.turn_id}:{})};
  const receipt = {request, key, epoch: state.epoch};
  state.pending.set(key, receipt);
  if(!request.user_message_id)state.sends.set(key,{request,phase:'sending'});
  return receipt;
}

export function settleSend(state, receipt, accepted, reason) {
  const pending = state.pending.get(receipt.key);
  if (pending?.request.request_id !== receipt.request.request_id || pending.epoch !== receipt.epoch) return false;
  state.pending.delete(receipt.key);
  if (accepted) state.retries.delete(receipt.key);
  else state.retries.set(receipt.key, receipt.request);
  if(!receipt.request.user_message_id&&!state.snapshot?.messages.some(message=>message.request_id===receipt.request.request_id))state.sends.set(receipt.key,{request:receipt.request,phase:accepted?'thinking':reason?.status?'failed':'uncertain',detail:reason?.message||''});
  // An acknowledgement clears only the exact submitted draft. New typing and
  // drafts belonging to a different assistant survive late acknowledgements.
  if (accepted && state.snapshot?.boot_id === receipt.request.boot_id && draft(state, receipt.key) === receipt.request.text) state.drafts.delete(receipt.key);
  return true;
}
