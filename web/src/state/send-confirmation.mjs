import {settleSend} from './conversation.mjs';

// A successful submit response is sufficient. Only an unknown submit result
// needs receipt reads, always for its captured scope and immutable request.
export function createSendConfirmation({state,client,changed,accepted,rejected=()=>{},cacheFailed=()=>{},canRetry=()=>true,clock=globalThis,now=()=>Date.now(),online=()=>globalThis.navigator?.onLine!==false,timeout=10000,delays=[750,1500,3000,6000,10000]}) {
  const operations=new Map();let closed=false,suspended=false;
  const current=op=>!closed&&operations.get(op.receipt)===op&&state.pending.get(op.receipt.key)===op.receipt;
  const owns=(op,batch)=>current(op)&&op.active===batch;
  function cancel(op){
    clock.clearTimeout(op.timer);op.timer=undefined;
    const batch=op.active;op.active=null;batch?.cancel?.();
  }
  function stop(op){cancel(op);operations.delete(op.receipt);}
  function finish(op,ok,reason){
    if(!current(op))return;
    if(settleSend(state,op.receipt,ok,reason)){if(ok)accepted(op.receipt);else rejected(op.receipt);}
    stop(op);changed();
  }
  async function bounded(op,batch,call){
    const controller=new AbortController();let timer;
    const ended=new Promise((_,reject)=>{
      batch.cancel=()=>{controller.abort();reject(Error('Client wait ended'));};
      timer=clock.setTimeout(batch.cancel,timeout);
    });
    try{
      if(!owns(op,batch))throw Error('Client wait ended');
      const value=await Promise.race([call({...op.scope,signal:controller.signal}),ended]);
      if(controller.signal.aborted||!owns(op,batch))throw Error('Client wait ended');
      return value;
    }catch(reason){
      if(controller.signal.aborted||!owns(op,batch))throw Error('Client wait ended',{cause:reason});
      throw reason;
    }finally{clock.clearTimeout(timer);batch.cancel=null;}
  }
  async function transmit(op,batch,retry=false){
    try{
      const result=await bounded(op,batch,async options=>{
        const value=await client.send(op.receipt.request,options);
        value?.committed?.catch(reason=>{if(!closed)cacheFailed(op.receipt,reason,value.retryCommit);});
        return value;
      });
      if(typeof result?.message_id!=='string'||typeof result?.duplicate!=='boolean')throw Error('Invalid message receipt');
      finish(op,true);
    }catch(reason){
      if(!owns(op,batch))return;
      // The adapter can report known server acceptance alongside a local cache
      // error. It must not turn that known outcome into another receipt query.
      if(reason?.accepted===true)finish(op,true);
      else if(!retry&&reason?.status>=400&&reason.status<500&&reason.status!==408)finish(op,false,reason);
      else op.unknown=true;
    }
  }
  async function query(op,batch){
    const value=await bounded(op,batch,options=>client.sendReceipt(op.receipt.request,options)),request=op.receipt.request;
    if(client.production&&(value?.runtime_id!==op.control||value.owner_id!==op.scope.owner))throw Error('Invalid acceptance owner');
    if(value?.request_id!==request.request_id||value.conversation_id!==request.conversation_id||
      (value.user_message_id||'')!==(request.user_message_id||'')||
      (value.retry_user_message_id||'')!==(request.retry_user_message_id||''))throw Error('Invalid acceptance identity');
    if(value.state==='accepted'&&typeof value.message_id==='string'){finish(op,true);return true;}
    if(value.state==='rejected'){finish(op,false,{status:409});return true;}
    if(value.state!=='unconfirmed')throw Error('Invalid acceptance state');
    op.blocked=false;return false;
  }
  function unknown(op,automatic=false){
    if(!current(op))return;
    op.unknown=true;op.receipt.phase='uncertain';
    const poll=automatic&&!suspended&&online()&&!op.blocked&&client.sendReceipt&&op.index<delays.length;
    op.receipt.recoverable=!poll;changed();
    if(poll)op.timer=clock.setTimeout(()=>{op.timer=undefined;check(op,false,true);},delays[op.index++]);
  }
  function check(op,explicit=false,automatic=false){
    if(!current(op)||!op.unknown||suspended||!online()||!client.sendReceipt)return;
    if(op.active)return op.active.promise;
    clock.clearTimeout(op.timer);op.timer=undefined;
    const batch=op.active={kind:'check'};
    op.receipt.recoverable=false;op.receipt.phase='confirming';changed();
    batch.promise=Promise.resolve().then(async()=>{
      if(!owns(op,batch))return;
      try{if(!await query(op,batch)&&owns(op,batch)&&explicit&&canRetry(op.receipt))await transmit(op,batch,true);}
      catch(reason){if(owns(op,batch)&&[401,403].includes(reason?.status))op.blocked=true;}
    }).finally(()=>{if(owns(op,batch)){op.active=null;unknown(op,automatic);}});
    return batch.promise;
  }
  function suspend({offline=false}={}){
    if(closed)return;suspended=true;
    for(const op of operations.values())if(current(op)){
      // Backgrounding alone does not discard a pending POST response. A real
      // offline transition cancels it and makes its acceptance result unknown.
      if(op.active?.kind==='submit'&&!offline)continue;
      cancel(op);op.resumeAt=0;unknown(op);
    }
  }
  return {
    start(receipt,capturedScope=client.scope?.()){
      const scope={...capturedScope},control=(client.spaces||[]).find(space=>space.id===scope.space)?.control_id||client.runtime?.runtime_id;
      const op={receipt,scope,control,index:0,resumeAt:0,unknown:false};operations.set(receipt,op);
      if(suspended||!online()){unknown(op);return;}
      const batch=op.active={kind:'submit'};
      batch.promise=transmit(op,batch).finally(()=>{if(owns(op,batch)){op.active=null;unknown(op,true);}});
    },
    // Reloaded drafts reconcile only the immutable request saved before POST.
    // Automatic checks stay read-only. Only an explicit Check and retry action
    // may resubmit the immutable request after an unconfirmed receipt.
    restore(receipt,capturedScope){
      const scope={...capturedScope},control=(client.spaces||[]).find(space=>space.id===scope.space)?.control_id||client.runtime?.runtime_id;
      const op={receipt,scope,control,index:0,resumeAt:0,unknown:true};operations.set(receipt,op);
      if(suspended||!online()){unknown(op);return;}
      check(op,false,true);
    },
    // A matching persisted SSE frame may confirm before the POST response.
    confirmed(receipt){const op=operations.get(receipt);if(op){stop(op);accepted(receipt);}},
    recover(receipt){const op=operations.get(receipt);if(op)return check(op,true);},
    resume(){
      if(closed)return Promise.resolve();
      if(!online()){suspend({offline:true});return Promise.resolve();}
      suspended=false;const checks=[];
      for(const op of operations.values())if(current(op)&&op.unknown&&op.receipt.recoverable&&!op.blocked&&!op.active&&now()>=op.resumeAt){
        op.resumeAt=now()+1000;checks.push(check(op));
      }
      return Promise.all(checks);
    },
    suspend,
    close(){closed=true;for(const op of operations.values())stop(op);},
  };
}
