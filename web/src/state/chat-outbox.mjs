import {createChatOutboxStore,outboxTerminal} from './chat-outbox-store.mjs';
import {validSnapshot} from './conversation.mjs';
import {supportsCapability} from './agent-links.mjs';
import {uploadChatOriginal} from './chat-upload.mjs';

// No draft or configuration API enters this dispatcher. A durable explicit
// submission is the only authorization to retry the immutable message identity.
export function createChatOutbox({client,changed=()=>{},cacheFailed=()=>{},storage=createChatOutboxStore({name:client.chatCacheName||'pot-chat-cache'}),clock=globalThis,now=()=>Date.now(),lease=30000,timeout=10000,interval=2000,online=()=>globalThis.navigator?.onLine!==false}){
  let closed=false,flight=null,rows=[],failure=null;const projections=new Map(),repaired=new Set(),originals=new Map();
  const channel=globalThis.BroadcastChannel?new BroadcastChannel('pot-chat-outbox:'+(client.chatCacheName||'pot-chat-cache')):null;
  const publish=()=>{if(!closed)changed();};
  const fail=problem=>Object.assign(Error(problem),{outboxProblem:problem});
  const projectionKey=value=>JSON.stringify([value.sequence,value.target.key,value.target.host,value.target.control,value.target.space,value.target.owner,value.target.conversation,value.request.request_id]);
  const ready=target=>!closed&&online()&&client.onlineReady!==false&&client.operationsReady!==false&&client.operationScopeReady?.(target)!==false;
  async function refresh(){rows=await storage.list();failure=null;publish();return rows;}
  async function bounded(work){const abort=new AbortController(),timer=clock.setTimeout(()=>abort.abort(),timeout);try{return await Promise.race([work(abort.signal),new Promise((_,reject)=>abort.signal.addEventListener('abort',()=>reject(Error('outbox_timeout')),{once:true}))]);}finally{clock.clearTimeout(timer);}}
  async function authority(value,signal){
    const {target}=value,scope={space:target.space,owner:target.owner,expectedControl:target.control,signal};
    if(client.runtime?.runtime_id!==target.host)throw fail('identityChanged');
    const space=client.spaces?.find(item=>item.id===target.space);
    if(!space){if(client.directoryGeneration===client.connectionGeneration)throw fail('identityChanged');return null;}
    if(space.control_id!==target.control)throw fail('identityChanged');
    if(client.onlineReady===false||client.operationsReady===false)return null;
    const assistant=client.assistants?.find(item=>item.space_id===target.space&&item.id===target.owner);
    if(!assistant){if(client.directoryGeneration===client.connectionGeneration)throw fail('assistantRemoved');return null;}
    if(assistant.offline||assistant.retained||!ready(scope))return null;
    if(!supportsCapability(assistant,'chat.send'))throw fail('permission');
    const generation=client.connectionGeneration,snapshot=await client.snapshot(scope);
    if(generation!==client.connectionGeneration||!ready(scope))return null;
    if(!validSnapshot(snapshot)||snapshot.runtime_id!==target.host||snapshot.owner_id!==JSON.stringify([target.space,target.owner]))throw fail('identityChanged');
    if(snapshot.conversation_id!==target.conversation)throw fail('conversationRetired');
    client.confirmSession?.(scope,generation);
    return {scope,snapshot,generation,assistant};
  }
  function acceptanceProjection(value,receipt,scope){
    if(!client.resources?.applyCommand)return Promise.resolve();
    const request=value.request,path=request.retry_user_message_id?'/api/v1/turn/retry':request.user_message_id?'/api/v1/turn/steer':'/api/v1/messages';
    return client.resources.applyCommand(path,{...scope,body:request,commandID:request.request_id},receipt,['conversation','assistants']);
  }
  function commitAcceptance(value,receipt,scope,commit){
    const key=projectionKey(value);if(projections.has(key))return projections.get(key);
    repaired.add(key);
    const retry=async()=>{await commit();await storage.releaseFiles(value);repaired.delete(key);channel?.postMessage('changed');await refresh();};
    const promise=retry().catch(reason=>{if(!closed)cacheFailed({...value,key:JSON.stringify([value.target.host,JSON.stringify([value.target.space,value.target.owner]),value.target.conversation])},reason,retry);}).finally(()=>projections.delete(key));
    projections.set(key,promise);return promise;
  }
  async function dispatch(candidate){
    const token=crypto.randomUUID();let value=await storage.claim(candidate.sequence,token,now(),lease);if(!value)return;
    await refresh();
    const update=async patch=>{const saved=await storage.update(value.sequence,token,patch);if(!saved)throw fail('claimLost');value=saved;return saved;};
    const renew=clock.setInterval(()=>storage.update(value.sequence,token,{claim:{token,until:now()+lease}}).catch(()=>{}),lease/3);
    try{
      await bounded(async signal=>{
        const context=await authority(value,signal);if(!context){await update({state:value.started?'uncertain':'queued',claim:null});return;}
        const {scope,snapshot,generation,assistant}=context;
        const ensureAuthority=()=>{
          if(signal.aborted)throw signal.reason;
          if(client.runtime?.runtime_id!==value.target.host||client.spaces?.find(item=>item.id===value.target.space)?.control_id!==value.target.control)throw fail('identityChanged');
          if(closed||generation!==client.connectionGeneration||!ready(scope))throw Error('outbox_authority_unconfirmed');
          const current=client.assistants?.find(item=>item.space_id===scope.space&&item.id===scope.owner);
          if(!current)throw fail('assistantRemoved');
          if(!supportsCapability(current,'chat.send'))throw fail('permission');
        };
        // A crash, lost response or expired claimant always reconciles the same
        // durable receipt before any retry. An absent receipt is not rejection;
        // exact-ID backend deduplication makes a still-in-flight copy safe.
        if(value.started){
          const receipt=await client.sendReceipt(value.request,scope),request=value.request;
          if(receipt.runtime_id!==value.target.control||receipt.owner_id!==value.target.owner||receipt.request_id!==request.request_id||receipt.conversation_id!==request.conversation_id||(receipt.user_message_id||'')!==(request.user_message_id||'')||(receipt.retry_user_message_id||'')!==(request.retry_user_message_id||''))throw fail('identityChanged');
          if(receipt.state==='accepted'){if(typeof receipt.message_id!=='string'||!receipt.message_id)throw fail('identityChanged');await update({state:'accepted',message_id:receipt.message_id,projection_pending:true,claim:null});commitAcceptance(value,{message_id:receipt.message_id,duplicate:true},scope,()=>acceptanceProjection(value,{message_id:receipt.message_id,duplicate:true},scope));return;}
          if(receipt.state==='rejected')throw fail('turnRetired');
          if(receipt.state!=='unconfirmed')throw fail('identityChanged');
        }
        if(value.request.user_message_id&&(!snapshot.running||snapshot.turn_id!==value.request.user_message_id))throw fail('turnRetired');
        if(value.request.attachments?.length){
          if(!supportsCapability(assistant,'chat.attachments'))throw fail('permission');
          const files=await storage.files(value.target.key,value.request.request_id);
          for(const attachment of value.request.attachments){
            const kind=attachment.mime_type==='application/pdf'?'pdf':'image';
            if(!snapshot.agent?.attachments?.[kind])throw fail('attachmentUnsupported');
            try{await uploadChatOriginal(client,attachment,files.find(file=>file.id===attachment.id)?.blob,scope,{signal,check:ensureAuthority});}catch(reason){if(reason.status&&![401,403].includes(reason.status))reason.outboxProblem='attachmentFailed';throw reason;}
          }
        }
        ensureAuthority();
        // Freeze the actual active turn once, before the first POST. Guidance
        // never follows a retired turn into a successor during reconciliation.
        if(!value.started){
          const request={...value.request,...(!value.request.retry_user_message_id&&!value.request.user_message_id&&snapshot.running?{user_message_id:snapshot.turn_id}:{})};
          await update({started:true,request,state:'delivering'});
        }
        ensureAuthority();
        const result=await client.send(value.request,scope);
        if(typeof result?.message_id!=='string'||!result.message_id||typeof result.duplicate!=='boolean')throw Error('Invalid message receipt');
        // Preserve originals until both business acceptance and its local
        // projection commit. A failed local commit retries locally, never POST.
        result.committed?.catch(()=>{});
        await update({state:'accepted',message_id:result.message_id,projection_pending:true,claim:null});
        let initial=true;commitAcceptance(value,{message_id:result.message_id,duplicate:result.duplicate},scope,()=>{if(initial&&result.committed){initial=false;return result.committed;}return result.retryCommit?result.retryCommit():acceptanceProjection(value,{message_id:result.message_id,duplicate:result.duplicate},scope);});
      });
    }catch(reason){
      const definitive=reason.outboxProblem&&reason.outboxProblem!=='claimLost'||reason.status>=400&&reason.status<500&&reason.status!==408&&reason.status!==429;
      await storage.update(value.sequence,token,{state:definitive?'failed':value.started?'uncertain':'queued',problem:reason.outboxProblem||(reason.code==='control_changed'?'identityChanged':null)||(definitive?[401,403].includes(reason.status)?'permission':'rejected':null),diagnostic:reason.message,claim:null}).catch(error=>{failure=error;});
    }finally{
      clock.clearInterval(renew);
      await storage.update(value.sequence,token,{state:value.started?'uncertain':'queued',claim:null}).catch(()=>{});
      channel?.postMessage('changed');await refresh();
    }
  }
  function wake(){
    if(closed)return Promise.resolve();if(flight)return flight;
    flight=(async()=>{
      await refresh();if(!online()||client.onlineReady===false||client.operationsReady===false)return;
      for(const value of rows)if(value.state==='accepted'&&value.projection_pending&&!repaired.has(projectionKey(value))&&client.runtime?.runtime_id===value.target.host&&client.spaces?.find(space=>space.id===value.target.space)?.control_id===value.target.control){
        const scope={space:value.target.space,owner:value.target.owner,expectedControl:value.target.control};commitAcceptance(value,{message_id:value.message_id,duplicate:true},scope,()=>acceptanceProjection(value,{message_id:value.message_id,duplicate:true},scope));
      }
      // One head per immutable target; unrelated assistants progress independently.
      const lanes=new Map();for(const row of rows)if(!outboxTerminal(row)&&!lanes.has(row.target.key))lanes.set(row.target.key,row);
      await Promise.all([...lanes.values()].filter(row=>row.state!=='failed'&&!client.assistants?.find(item=>item.space_id===row.target.space&&item.id===row.target.owner)?.offline).map(dispatch));
    })().catch(error=>{failure=error;publish();}).finally(()=>{flight=null;});return flight;
  }
  if(channel)channel.onmessage=()=>wake();
  const timer=clock.setInterval(wake,interval);queueMicrotask(wake);
  return {
    wake,refresh,rows:()=>rows,error:()=>failure,
    async edit(row,text){const value=await storage.edit(row,text);channel?.postMessage('changed');await refresh();return value;},
    async retry(row){const value=await storage.retry(row);channel?.postMessage('changed');await refresh();wake();return value;},
    async cancel(row){const value=await storage.cancel(row);channel?.postMessage('changed');await refresh();return value;},
    acquireOriginal(target,request,attachment,scope){
      const key=JSON.stringify([target,request,attachment.id]);let entry=originals.get(key);
      if(!entry){entry={references:0};originals.set(key,entry);}entry.references++;
      let released=false;const release=()=>{if(released)return;released=true;if(entry.references<=0)return;if(--entry.references>0)return;originals.delete(key);if(entry.url)URL.revokeObjectURL(entry.url);entry.serverRelease?.();};
      return {url(){
        if(!entry.pending)entry.pending=(async()=>{
          const file=(await storage.files(target,request)).find(file=>file.id===attachment.id);
          if(file){const url=URL.createObjectURL(file.blob);entry.url=url;if(entry.references===0||closed)URL.revokeObjectURL(url);return url;}
          const [host,control]=JSON.parse(target),captured={...scope,expectedControl:control};if(client.runtime?.runtime_id!==host||client.spaces?.find(space=>space.id===scope.space)?.control_id!==control)throw fail('identityChanged');
          entry.serverRelease=client.retainAttachment?.(attachment,captured);const pending=client.attachmentURL(attachment,captured);if(!entry.references||closed)entry.serverRelease?.();return pending;
        })().catch(error=>{entry.serverRelease?.();entry.serverRelease=null;entry.pending=null;throw error;});return entry.pending;
      },release};
    },
    close(){closed=true;clock.clearInterval(timer);channel?.close();for(const entry of originals.values()){entry.references=0;if(entry.url)URL.revokeObjectURL(entry.url);entry.serverRelease?.();}originals.clear();Promise.allSettled([flight,...projections.values()]).finally(()=>storage.close());},
  };
}
