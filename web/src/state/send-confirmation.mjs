import {settleSend} from './conversation.mjs';

// A request timeout ends only the client's wait. Acceptance is reconciled using
// its captured scope and immutable request, never the current editor or turn.
export function createSendConfirmation({state,client,changed,accepted,rejected=()=>{},clock=globalThis,now=()=>Date.now(),online=()=>globalThis.navigator?.onLine!==false,timeout=10000,delays=[750,1500,3000,6000,10000]}) {
  const operations=new Map();
  let closed=false,suspended=false;
  const current=op=>!closed && operations.get(op.receipt)===op && state.pending.get(op.receipt.key)===op.receipt;
  const owns=(op,generation)=>current(op)&&op.generation===generation&&!suspended;
  function cancel(op){
    // Revoke every asynchronous owner before aborting. An abort listener may
    // settle synchronously, and its finally must not clear a newer query.
    op.generation++;op.polling=op.check=op.recovery=null;
    clock.clearTimeout(op.timer);op.timer=undefined;
    for(const end of op.waits)end();op.waits.clear();
  }
  function stop(op){cancel(op);operations.delete(op.receipt);}
  function finish(op,ok,reason){
    if(!current(op))return;
    if(settleSend(state,op.receipt,ok,reason)){
      if(ok)accepted(op.receipt);
      else rejected(op.receipt);
    }
    stop(op);changed();
  }
  async function bounded(op,call,generation=op.generation){
    const controller=new AbortController();let timer,cancel;
    try {
      if(!owns(op,generation))throw Error('Client wait ended');
      const value=await Promise.race([call({...op.scope,signal:controller.signal}),new Promise((_,reject)=>{
        cancel=()=>{controller.abort();reject(Error('Client wait ended'));};
        op.waits.add(cancel);timer=clock.setTimeout(cancel,timeout);
      })]);
      if(controller.signal.aborted||!owns(op,generation))throw Error('Client wait ended');
      return value;
    } catch(reason){
      if(controller.signal.aborted||!owns(op,generation))throw Error('Client wait ended',{cause:reason});
      throw reason;
    } finally {clock.clearTimeout(timer);op.waits.delete(cancel);}
  }
  async function transmit(op,recovery=false){
    const generation=op.generation;
    try {
      const result=await bounded(op,options=>client.send(op.receipt.request,options));
      if(typeof result?.message_id!=='string'||typeof result?.duplicate!=='boolean')throw Error('Invalid message receipt');
      finish(op,true);
    } catch(reason){
      if(!owns(op,generation))return;
      // An explicit rejection of the original call is definitive. Once a wait
      // was lost, a later rejection alone cannot disprove earlier acceptance.
      if(!recovery && reason?.status>=400 && reason.status<500 && reason.status!==408)finish(op,false,reason);
      else {op.receipt.phase='uncertain';changed();}
    }
  }
  function query(op){
    if(op.check)return op.check.promise;
    const check=op.check={generation:op.generation};
    const active=()=>owns(op,check.generation)&&op.check===check;
    check.promise=Promise.resolve().then(async()=>{
      if(!active())return;
      const value=await bounded(op,options=>client.sendReceipt(op.receipt.request,options),check.generation);
      if(!active())return;
      const request=op.receipt.request;
      if(client.production && (value?.runtime_id!==op.control || value.owner_id!==op.scope.owner))throw Error('Invalid acceptance owner');
      if(value?.request_id!==request.request_id || value.conversation_id!==request.conversation_id ||
        (value.user_message_id||'')!==(request.user_message_id||'') ||
        (value.retry_user_message_id||'')!==(request.retry_user_message_id||''))throw Error('Invalid acceptance identity');
      if(value.state==='accepted' && typeof value.message_id==='string'){finish(op,true);return true;}
      if(value.state==='rejected'){finish(op,false,{status:409});return true;}
      if(value.state!=='unconfirmed')throw Error('Invalid acceptance state');
      op.blocked=false;return false;
    }).catch(reason=>{
      if(active()&&[401,403].includes(reason?.status))op.blocked=true;
      throw reason;
    }).finally(()=>{if(op.check===check)op.check=null;});
    return check.promise;
  }
  function schedule(op){
    if(!current(op)||suspended||!client.sendReceipt)return;
    if(op.index===delays.length||op.blocked){op.polling=null;op.receipt.phase='uncertain';op.receipt.recoverable=true;changed();return;}
    const polling=op.polling;
    op.timer=clock.setTimeout(async()=>{
      op.timer=undefined;
      if(!current(op)){stop(op);return;}
      if(!polling||op.polling!==polling)return;
      try {await query(op);}catch{/* An unavailable read proves neither outcome. */}
      if(current(op)&&op.polling===polling)schedule(op);
    },delays[op.index++]);
  }
  function recover(op,explicit){
    if(!current(op)||suspended||!online()||!client.sendReceipt)return;
    if(op.recovery)return op.recovery.promise;
    op.polling=null;clock.clearTimeout(op.timer);op.timer=undefined;
    const recovery=op.recovery={generation:op.generation};
    const active=()=>owns(op,recovery.generation)&&op.recovery===recovery;
    op.receipt.recoverable=false;op.receipt.phase='confirming';changed();
    recovery.promise=Promise.resolve().then(async()=>{
      if(!active())return;
      // Automatic recovery only reads. A user-requested retry retains the
      // original immutable payload and may transmit after an unconfirmed read.
      try{if(!await query(op)&&active()&&explicit)await transmit(op,true);}
      catch{/* Keep the fixed attempt when acceptance cannot be established. */}
      finally{if(active()){op.recovery=null;op.receipt.phase='uncertain';op.receipt.recoverable=true;changed();}}
    });
    return recovery.promise;
  }
  function suspend(){
    if(closed||suspended)return;suspended=true;
    for(const op of operations.values())if(current(op)){op.resumeAt=0;op.receipt.phase='uncertain';op.receipt.recoverable=true;cancel(op);}
    changed();
  }
  return {
    start(receipt){
      const scope={...client.scope?.()};
      const control=(client.spaces||[]).find(space=>space.id===scope.space)?.control_id||client.runtime?.runtime_id;
      const op={receipt,scope,control,waits:new Set(),index:0,generation:0,polling:{},resumeAt:0};operations.set(receipt,op);
      if(suspended||!online()){receipt.phase='uncertain';receipt.recoverable=true;changed();return;}
      transmit(op);schedule(op);
    },
    // Snapshot reconciliation already settled state; cancel only this attempt's
    // client waits and clean its submitted attachments exactly once.
    confirmed(receipt){const op=operations.get(receipt);if(op){stop(op);accepted(receipt);}},
    recover(receipt){const op=operations.get(receipt);if(op)return recover(op,true);},
    resume(){
      if(closed)return Promise.resolve();
      if(!online()){suspend();return Promise.resolve();}
      suspended=false;const checks=[];
      for(const op of operations.values())if(current(op)&&op.receipt.recoverable&&!op.blocked&&!op.recovery&&now()>=op.resumeAt){
        // One read per recovery burst, with a short cooldown for repeated focus
        // events. No timer rearms this budget or restarts the initial polling.
        op.resumeAt=now()+1000;checks.push(recover(op,false));
      }
      return Promise.all(checks);
    },
    suspend,
    close(){closed=true;for(const op of operations.values())stop(op);},
  };
}
