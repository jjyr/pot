import {settleSend} from './conversation.mjs';

// A request timeout ends only the client's wait. Acceptance is reconciled using
// its captured scope and immutable request, never the current editor or turn.
export function createSendConfirmation({state,client,changed,accepted,rejected=()=>{},clock=globalThis,timeout=10000,delays=[750,1500,3000,6000,10000]}) {
  const operations=new Map();
  let closed=false;
  const current=op=>!closed && state.pending.get(op.receipt.key)===op.receipt;
  function stop(op){
    clock.clearTimeout(op.timer);
    for(const cancel of op.waits)cancel();
    operations.delete(op.receipt);
  }
  function finish(op,ok,reason){
    if(!current(op))return;
    if(settleSend(state,op.receipt,ok,reason)){
      if(ok)accepted(op.receipt);
      else rejected(op.receipt);
    }
    stop(op);changed();
  }
  async function bounded(op,call){
    const controller=new AbortController();let timer,cancel;
    try {
      return await Promise.race([call({...op.scope,signal:controller.signal}),new Promise((_,reject)=>{
        cancel=()=>{controller.abort();reject(Error('Client wait ended'));};
        op.waits.add(cancel);timer=clock.setTimeout(cancel,timeout);
      })]);
    } finally {clock.clearTimeout(timer);op.waits.delete(cancel);}
  }
  async function transmit(op,recovery=false){
    try {
      const result=await bounded(op,options=>client.send(op.receipt.request,options));
      if(typeof result?.message_id!=='string'||typeof result?.duplicate!=='boolean')throw Error('Invalid message receipt');
      finish(op,true);
    } catch(reason){
      if(!current(op))return;
      // An explicit rejection of the original call is definitive. Once a wait
      // was lost, a later rejection alone cannot disprove earlier acceptance.
      if(reason?.accepted===true)finish(op,true);
      else if(!recovery && reason?.status>=400 && reason.status<500 && reason.status!==408)finish(op,false,reason);
      else {op.receipt.phase='uncertain';changed();}
    }
  }
  async function query(op){
    const value=await bounded(op,options=>client.sendReceipt(op.receipt.request,options));
    const request=op.receipt.request;
    if(client.production && (value?.runtime_id!==op.control || value.owner_id!==op.scope.owner))throw Error('Invalid acceptance owner');
    if(value?.request_id!==request.request_id || value.conversation_id!==request.conversation_id ||
      (value.user_message_id||'')!==(request.user_message_id||'') ||
      (value.retry_user_message_id||'')!==(request.retry_user_message_id||''))throw Error('Invalid acceptance identity');
    if(value.state==='accepted' && typeof value.message_id==='string'){finish(op,true);return true;}
    if(value.state==='rejected'){finish(op,false,{status:409});return true;}
    if(value.state!=='unconfirmed')throw Error('Invalid acceptance state');
    return false;
  }
  function schedule(op){
    if(!current(op)||!client.sendReceipt)return;
    if(op.index===delays.length){op.receipt.phase='uncertain';op.receipt.recoverable=true;changed();return;}
    op.timer=clock.setTimeout(async()=>{
      if(!current(op)){stop(op);return;}
      try {await query(op);}catch{/* An unavailable read proves neither outcome. */}
      if(current(op))schedule(op);
    },delays[op.index++]);
  }
  return {
    start(receipt,capturedScope=client.scope?.()){
      const scope={...capturedScope};
      const control=(client.spaces||[]).find(space=>space.id===scope.space)?.control_id||client.runtime?.runtime_id;
      const op={receipt,scope,control,waits:new Set(),index:0};operations.set(receipt,op);
      transmit(op).finally(()=>schedule(op));
    },
    // A reloaded draft may already have been accepted. Reconcile its captured
    // immutable request without issuing a POST, including on explicit recovery.
    restore(receipt,scope){
      scope={...scope};
      const control=(client.spaces||[]).find(space=>space.id===scope.space)?.control_id||client.runtime?.runtime_id;
      const op={receipt,scope,control,waits:new Set(),index:0,readOnly:true};operations.set(receipt,op);
      query(op).catch(()=>{}).finally(()=>schedule(op));
    },
    // Snapshot reconciliation already settled state; cancel only this attempt's
    // client waits and clean its submitted attachments exactly once.
    confirmed(receipt){const op=operations.get(receipt);if(op){stop(op);accepted(receipt);}},
    async recover(receipt){
      const op=operations.get(receipt);
      if(!op||!current(op)||op.recovering)return;
      op.recovering=true;receipt.recoverable=false;receipt.phase='confirming';changed();
      try {if(!await query(op) && current(op)&&!op.readOnly)await transmit(op,true);}
      catch{/* Keep the fixed attempt for another explicit recovery. */}
      finally{op.recovering=false;if(current(op)){receipt.phase='uncertain';receipt.recoverable=true;changed();}}
    },
    close(){closed=true;for(const op of operations.values())stop(op);},
  };
}
