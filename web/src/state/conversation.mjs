import {mergeUIFrame} from './chat-history.mjs';
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
    && (Array.isArray(value.replies) && value.replies.every(reply=>reply&&typeof reply.input_id==='string'&&reply.input_id.length>0&&['pending','queued','running','waiting','paused','cancelling','completed','failed','cancelled','unknown'].includes(reply.state)&&(reply.error===undefined||typeof reply.error==='string')&&(reply.uncertain===undefined||typeof reply.uncertain==='boolean')))
    && Array.isArray(value.messages)
    && value.messages.every(message => message && typeof message.id === 'string'
      && ['user', 'assistant', 'system', 'tool'].includes(message.role)
      && typeof message.text === 'string' && typeof message.status === 'string'
      && (message.error===undefined||typeof message.error==='string')
      && (message.reply_to===undefined||typeof message.reply_to==='string')
      && (message.references===undefined||Array.isArray(message.references)&&message.references.every(ref=>ref&&typeof ref.message_id==='string'&&(ref.quote===undefined||typeof ref.quote==='string'))));
}

export function createConversationState() {
  return {epoch: 0, snapshot: null, drafts: new Map(), draftVersions: new Map(), referenceVersions: new Map(), referenceDrafts: new Map(), pending: new Map(), retries: new Map(), sends: new Map(), seenRequests: new Set()};
}

export function beginSubscription(state) {
  state.epoch++;
  // Navigation and reconnects cannot turn an unknown send into a new attempt.
  state.snapshot = null;
  return state.epoch;
}

export function messageConfirmsSend(message, receipt) {
  const request=receipt.request;
  return !request.retry_user_message_id && message.role==='user' && (request.user_message_id
    ? message.accepted_request_id===request.request_id && message.steering && message.accepted_turn_id===request.user_message_id
    : message.request_id===request.request_id);
}

export function acceptSnapshot(state, snapshot, epoch, {committed=false,receivedMessages=snapshot?.messages}={}) {
  if (epoch !== state.epoch || !validSnapshot(snapshot)) return false;
  const previous = state.snapshot;
  // A different identity/boot is established only by a fresh subscription. Late
  // events may not silently navigate the user into another owner's conversation.
  if (previous && (ownerKey(previous) !== ownerKey(snapshot) || previous.boot_id !== snapshot.boot_id)) return false;
  if (previous && BigInt(snapshot.cursor) < BigInt(previous.cursor)) return false;
  if(previous && snapshot.cursor === previous.cursor && snapshot.kind !== 'reset' && !committed) return false;
  if(!committed)snapshot = mergeUIFrame(previous, snapshot, state.uiAnchor);
  else if(previous){
    // Disk reads clone records. Reuse unchanged active rows so a token update
    // does not invalidate every mounted row's measurement and content view.
    const old=new Map(previous.messages.map(message=>[message.id,message]));
    snapshot={...snapshot,messages:snapshot.messages.map(message=>{
      const value=old.get(message.id);
      if(!value||value.sequence!==message.sequence||!!value.context_only!==!!message.context_only)return message;
      if(message.record_id!==undefined||value.record_id!==undefined){
        const same=value.record_id===message.record_id&&value.record_revision===message.record_revision&&value.id===message.id&&value.sequence===message.sequence&&!!value.context_only===!!message.context_only&&value.viewKey===message.viewKey&&value.mount_order===message.mount_order&&value.local_only===message.local_only&&value.delivery?.revision===message.delivery?.revision;
        return same?value:message;
      }
      return message.version!==undefined&&value.version===message.version||message.version===undefined&&JSON.stringify(value)===JSON.stringify(message)?value:message;
    })};
  }
  state.snapshot = snapshot;
  const key=ownerKey(snapshot),pending=state.pending.get(key),send=state.sends.get(key);
  for(const message of receivedMessages)if(message.request_id&&!message.local_only){const receipt=JSON.stringify([key,message.request_id]);state.seenRequests.delete(receipt);state.seenRequests.add(receipt);}
  while(state.seenRequests.size>256)state.seenRequests.delete(state.seenRequests.values().next().value);
  if(send&&(state.seenRequests.has(JSON.stringify([key,send.request.request_id]))||snapshot.messages.some(message=>!message.local_only&&message.request_id===send.request.request_id)))state.sends.delete(key);
  if(pending && receivedMessages.some(message=>!message.local_only&&messageConfirmsSend(message,pending)))settleSend(state,pending,true);
  return true;
}

export function draft(state, key = state.snapshot && ownerKey(state.snapshot)) {
  return state.drafts.get(key) || '';
}

export function setDraft(state, text, key = state.snapshot && ownerKey(state.snapshot)) {
  if (key && draft(state,key)!==text) { state.drafts.set(key, text); state.draftVersions.set(key,(state.draftVersions.get(key)||0)+1); state.draftChanged?.(key); }
}

export function draftReferences(state, key = state.snapshot && ownerKey(state.snapshot)) { return state.referenceDrafts.get(key) || []; }
export function setDraftReferences(state, refs, key = state.snapshot && ownerKey(state.snapshot)) { if(key && JSON.stringify(draftReferences(state,key))!==JSON.stringify(refs)){state.referenceDrafts.set(key,refs.map(ref=>({...ref})));state.referenceVersions.set(key,(state.referenceVersions.get(key)||0)+1);state.draftChanged?.(key);} }

export function beginSend(state, requestID, {allowRunning=false,attachments=[],retryUserMessageID}={}) {
  if (!state.snapshot || (state.snapshot.running&&!allowRunning)) return null;
  const key = ownerKey(state.snapshot);
  const text = draft(state, key);
  const references = draftReferences(state,key);
  if ((!text.trim() && !attachments.length) || state.pending.has(key)) return null;
  const retry = state.retries.get(key);
  const sameTarget=retry?.user_message_id===(state.snapshot.running?state.snapshot.turn_id:undefined) && (retry?.retry_user_message_id||undefined)===(retryUserMessageID||undefined);
  const request = sameTarget && retry?.text === text && JSON.stringify(retry.references||[])===JSON.stringify(references) && JSON.stringify(retry.attachments||[])===JSON.stringify(attachments) && retry.boot_id === state.snapshot.boot_id ? retry
    : {request_id: requestID, boot_id: state.snapshot.boot_id, conversation_id: state.snapshot.conversation_id, text,...(references.length?{references:references.map(ref=>({...ref}))}:{}),...(attachments.length?{attachments:attachments.map(item=>({...item}))}:{}),...(retryUserMessageID?{retry_user_message_id:retryUserMessageID}:{}),...(state.snapshot.running?{user_message_id:state.snapshot.turn_id}:{})};
  const receipt = {request, key, epoch: state.epoch, phase:'sending', draftVersion:state.draftVersions.get(key)||0, referenceVersion:state.referenceVersions.get(key)||0};
  state.pending.set(key, receipt);
  if(!request.user_message_id)state.sends.set(key,{request,phase:'sending'});
  return receipt;
}

export function settleSend(state, receipt, accepted, reason) {
  const pending = state.pending.get(receipt.key);
  if (pending !== receipt) return false;
  state.pending.delete(receipt.key);
  if (accepted) state.retries.delete(receipt.key);
  else state.retries.set(receipt.key, receipt.request);
  if(!receipt.request.user_message_id&&!state.seenRequests.has(JSON.stringify([receipt.key,receipt.request.request_id]))&&!state.snapshot?.messages.some(message=>message.request_id===receipt.request.request_id))state.sends.set(receipt.key,{request:receipt.request,phase:accepted?'thinking':reason?.status?'failed':'uncertain',detail:reason?.message||''});
  if(accepted && (state.referenceVersions.get(receipt.key)||0)===receipt.referenceVersion && JSON.stringify(draftReferences(state,receipt.key))===JSON.stringify(receipt.request.references||[]))state.referenceDrafts.delete(receipt.key);
  // An acknowledgement clears only the exact submitted draft. New typing and
  // drafts belonging to a different assistant survive late acknowledgements.
  if (accepted && (state.draftVersions.get(receipt.key)||0)===receipt.draftVersion && draft(state, receipt.key) === receipt.request.text) state.drafts.delete(receipt.key);
  return true;
}
