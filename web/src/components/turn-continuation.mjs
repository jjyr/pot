import {translate} from '../locales/messages.mjs';

const node=(tag,className,text)=>{const value=document.createElement(tag);value.className=className;if(text!==undefined)value.textContent=text;return value;};

// Continuation captures the original owner/input, independently of the composer
// draft. An ambiguous acknowledgment retries the same command identity.
export function createTurnContinuation({client,locale,onContinued,changed}) {
  const root=node('div','turn-continuation-error');
  root.hidden=true;root.setAttribute('role','alert');
  const attempts=new Map();let current,signature;
  const t=key=>translate(locale(),key);
  function render(){
    const attempt=current&&attempts.get(current.key);
    const next=JSON.stringify([current?.key,current?.summary,current?.recovery,current?.connected,attempt?.pending,attempt?.accepted,attempt?.error?.code,attempt?.error?.message,locale()]);
    if(next===signature)return;signature=next;
    root.hidden=!current||!!attempt?.accepted||!attempt?.error;
    if(!current)return;
    root.replaceChildren();
    if(attempt?.error){
      root.append(node('p','',t(attempt.error.code==='turn_recovery_required'?'continuation.unresolved':attempt.error.code==='turn_continuation_unavailable'?'continuation.stale':'continuation.failed')));
      if(attempt.error.message){const details=node('details','reply-error-details');details.append(node('summary','',t('errorDetails')),node('pre','',attempt.error.message));root.append(details);}
    }
  }
  function state(){
    const attempt=current&&attempts.get(current.key);
    return {available:!!current&&!attempt?.accepted,pending:!!attempt?.pending,
      disabled:!current?.connected||!!attempt?.pending||!!attempt?.accepted,
      label:t(attempt?.pending?'continuation.pending':current?.summary?'continuation.summaryContinue':'continuation.continue')};
  }
  async function resume(){
    if(!current||state().disabled)return;
    const captured={...current},attempt=attempts.get(captured.key)||{id:crypto.randomUUID()};
    attempts.set(captured.key,attempt);attempt.pending=true;attempt.error=null;render();changed?.();
    try{
      const receipt=await client.continueTurn({request_id:attempt.id,user_message_id:captured.target,attempt_id:captured.attemptID},captured.scope);
      if(receipt?.message_id!==captured.target||typeof receipt.duplicate!=='boolean')throw Error('Invalid continuation receipt');
      attempt.accepted=true;
      if(current?.key===captured.key)onContinued?.(captured.scope);
    }catch(reason){attempt.error=reason;}
    finally{attempt.pending=false;render();changed?.();}
  }
  return {root,state,resume,update(snapshot,{connected=false}={}){
    const continuation=snapshot?.continuation;
    current=continuation&&!snapshot.running?{key:JSON.stringify([snapshot.runtime_id,snapshot.owner_id,snapshot.conversation_id,continuation.user_message_id,continuation.attempt_id]),target:continuation.user_message_id,attemptID:continuation.attempt_id,summary:continuation.summary,recovery:continuation.recovery_required,connected,scope:{...client.scope()}}:null;
    render();
  }};
}
