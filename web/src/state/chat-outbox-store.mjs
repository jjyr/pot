import {createIndexedStore,idbRequest} from './indexed-store.mjs';
import {recordStores,putLocalRecord,syncDeliveryRecord,hydrateDelivery,clearRecordFiles,recordCacheKey} from './chat-record-store.mjs';

export function outboxTarget(client,scope,snapshot){
  const space=(client.spaces||[]).find(value=>value.id===scope.space),control=space?.control_id;
  if(!client.runtime?.runtime_id||!control||!scope.space||!scope.owner||!snapshot?.conversation_id||snapshot.owner_id!==JSON.stringify([scope.space,scope.owner])||snapshot.runtime_id!==client.runtime.runtime_id)throw Error('outbox_identity_missing');
  const target={host:client.runtime.runtime_id,control,space:scope.space,owner:scope.owner,conversation:snapshot.conversation_id};
  return {...target,key:JSON.stringify(Object.values(target))};
}
export const outboxTerminal=value=>['accepted','cancelled'].includes(value.state);
export const outboxIdentityKey=value=>JSON.stringify([value.sequence,value.target.key,value.target.host,value.target.control,value.target.space,value.target.owner,value.target.conversation,value.request.request_id]);
const sameSubmission=(value,expected)=>value.request.request_id===expected.request.request_id&&['key','host','control','space','owner','conversation'].every(key=>value.target[key]===expected.target[key]);

function rawJob(value){
  const {request,record_id,...rest}=value;
  return {...rest,request_id:value.request.request_id,record_ref:[value.target.key,'input:'+value.request.request_id],schema_version:2,active:(!outboxTerminal(value)||(value.state==='accepted'&&value.projection_pending))?1:0};
}

export async function enqueueOutbox(tx,draftKey,value,files){
  const drafts=tx.objectStore('composerDrafts'),originals=tx.objectStore('composerFiles');
  const draft=await idbRequest(drafts.get(draftKey));
  const hydrated={...value,state:'queued',revision:1,started:false,claim:null,request_id:value.request.request_id};
  const sequence=await idbRequest(tx.objectStore('chatOutbox').add(rawJob(hydrated)));
  hydrated.sequence=sequence;
  hydrated.record_ref=[value.target.key,'input:'+value.request.request_id];
  await putLocalRecord(tx,hydrated,sequence);
  await syncDeliveryRecord(tx,hydrated);
  const record_id='input:'+value.request.request_id;
  for(const file of files)tx.objectStore('chatRecordFiles').put({...file,target:value.target.key,record_id});
  if(draft){
    if(draft.draftVersion===value.draftVersion&&draft.text===value.request.text)draft.text='';
    if(draft.referenceVersion===value.referenceVersion&&JSON.stringify(draft.references)===JSON.stringify(value.request.references||[]))draft.references=[];
    const ids=new Set((value.request.attachments||[]).map(file=>file.id));
    draft.attachments=draft.attachments.filter(file=>!ids.has(file.attachment.id));
    for(const id of ids)originals.delete([draftKey,id]);
    drafts.put(draft);
  }
  return sequence;
}

export function createChatOutboxStore(options={}){
  const db=createIndexedStore(options);
  const channel=globalThis.BroadcastChannel?new BroadcastChannel('pot-chat-commits:'+(options.name||'pot-chat-cache')):null;
  channel?.unref?.();
  const published=async promise=>{const result=await promise;if(result)channel?.postMessage(recordCacheKey(result.target));return result;};
  const mutate=(sequence,work)=>published(db.transaction(recordStores,'readwrite',async tx=>{
    const store=tx.objectStore('chatOutbox'),raw=await idbRequest(store.get(sequence));
    if(!raw)return null;
    const value=await hydrateDelivery(tx,raw);
    if(!work(value))return null;
    value.revision++;
    await syncDeliveryRecord(tx,value);
    store.put(rawJob(value));
    return value;
  }));
  return {
    list:()=>db.transaction(['chatOutbox','chatRecords'],'readonly',async tx=>{
      const raws=await idbRequest(tx.objectStore('chatOutbox').index('active').getAll(1));
      return Promise.all(raws.map(raw=>hydrateDelivery(tx,raw)));
    }),
    listSaved:()=>db.transaction(['chatOutbox','chatRecords'],'readonly',async tx=>{
      const raws=await idbRequest(tx.objectStore('chatOutbox').getAll());
      return Promise.all(raws.map(raw=>hydrateDelivery(tx,raw)));
    }),
    files:(target,request)=>db.transaction(['chatRecordFiles'],'readonly',tx=>idbRequest(tx.objectStore('chatRecordFiles').index('record').getAll([target,'input:'+request]))),
    async claim(sequence,token,now,lease){
      return published(db.transaction(recordStores,'readwrite',async tx=>{
        const store=tx.objectStore('chatOutbox'),raw=await idbRequest(store.get(sequence));
        if(!raw)return null;
        const value=await hydrateDelivery(tx,raw);
        if(!value||outboxTerminal(value)||value.state==='failed'||(value.claim&&value.claim.until>now))return null;
        const lane=await idbRequest(store.index('target').getAll(value.target.key));
        if(lane.some(item=>item.sequence<sequence&&!['accepted','cancelled'].includes(item.state)))return null;
        value.claim={token,until:now+lease};
        value.state=value.started?'uncertain':'delivering';
        value.revision++;
        await syncDeliveryRecord(tx,value);
        store.put(rawJob(value));
        return value;
      }));
    },
    update(sequence,token,patch){return mutate(sequence,value=>{if(value.claim?.token!==token)return false;Object.assign(value,patch);return true;});},
    edit(expected,text){return mutate(expected.sequence,value=>{if(!sameSubmission(value,expected)||value.started||value.claim||value.revision!==expected.revision||!['queued','failed'].includes(value.state)||(!text.trim()&&!value.request.attachments?.length)||new TextEncoder().encode(text).byteLength>65536)return false;value.request={...value.request,text};value.state='queued';value.problem=null;return true;});},
    retry(expected){return mutate(expected.sequence,value=>{if(!sameSubmission(value,expected)||value.claim||value.state!=='failed'||value.revision!==expected.revision)return false;value.state=value.started?'uncertain':'queued';value.problem=null;return true;});},
    cancel(expected){return published(db.transaction(recordStores,'readwrite',async tx=>{
      const store=tx.objectStore('chatOutbox'),raw=await idbRequest(store.get(expected.sequence));
      if(!raw)return null;
      const value=await hydrateDelivery(tx,raw);
      if(!value||!sameSubmission(value,expected)||value.started||value.claim||value.revision!==expected.revision||outboxTerminal(value))return null;
      value.state='cancelled';value.revision++;
      const record=await syncDeliveryRecord(tx,value);
      await clearRecordFiles(tx,record);
      store.put(rawJob(value));
      return value;
    }));},
    releaseFiles(expected){return published(db.transaction(recordStores,'readwrite',async tx=>{
      const store=tx.objectStore('chatOutbox'),raw=await idbRequest(store.get(expected.sequence));
      if(!raw)return null;
      const value=await hydrateDelivery(tx,raw);
      if(!value||!sameSubmission(value,expected)||value.state!=='accepted')return null;
      if(!value.projection_pending)return null;
      value.projection_pending=false;value.revision++;
      const record=await syncDeliveryRecord(tx,value);
      await clearRecordFiles(tx,record);
      store.put(rawJob(value));
      return value;
    }));},
    close:()=>{channel?.close();db.close();},
  };
}
