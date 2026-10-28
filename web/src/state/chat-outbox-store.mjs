import {createIndexedStore,idbRequest} from './indexed-store.mjs';

export function outboxTarget(client,scope,snapshot){
  const space=(client.spaces||[]).find(value=>value.id===scope.space),control=space?.control_id;
  if(!client.runtime?.runtime_id||!control||!scope.space||!scope.owner||!snapshot?.conversation_id||snapshot.owner_id!==JSON.stringify([scope.space,scope.owner])||snapshot.runtime_id!==client.runtime.runtime_id)throw Error('outbox_identity_missing');
  const target={host:client.runtime.runtime_id,control,space:scope.space,owner:scope.owner,conversation:snapshot.conversation_id};
  return {...target,key:JSON.stringify(Object.values(target))};
}
export const outboxTerminal=value=>['accepted','cancelled'].includes(value.state);
export const outboxIdentityKey=value=>JSON.stringify([value.sequence,value.target.key,value.target.host,value.target.control,value.target.space,value.target.owner,value.target.conversation,value.request.request_id]);
const sameSubmission=(value,expected)=>value.request.request_id===expected.request.request_id&&['key','host','control','space','owner','conversation'].every(key=>value.target[key]===expected.target[key]);

// Used by the draft writer in its serialized lane. The transaction transfers
// ownership of originals and clears only the submitted editor versions.
export async function enqueueOutbox(tx,draftKey,value,files){
  const drafts=tx.objectStore('composerDrafts'),originals=tx.objectStore('composerFiles');
  const draft=await idbRequest(drafts.get(draftKey));
  const sequence=await idbRequest(tx.objectStore('chatOutbox').add({...value,schema_version:1,state:'queued',revision:1,started:false,claim:null}));
  for(const file of files)tx.objectStore('chatOutboxFiles').put({target:value.target.key,request:value.request.request_id,...file});
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
  const mutate=(sequence,work)=>db.transaction(['chatOutbox'],'readwrite',async tx=>{
    const store=tx.objectStore('chatOutbox'),value=await idbRequest(store.get(sequence));
    if(!value)return null;
    const result=work(value);if(!result)return null;
    value.revision++;store.put(value);return value;
  });
  return {
    list:()=>db.transaction(['chatOutbox'],'readonly',tx=>idbRequest(tx.objectStore('chatOutbox').getAll())),
    files:(target,request)=>db.transaction(['chatOutboxFiles'],'readonly',tx=>idbRequest(tx.objectStore('chatOutboxFiles').index('request').getAll([target,request]))),
    async claim(sequence,token,now,lease){
      return db.transaction(['chatOutbox'],'readwrite',async tx=>{
        const store=tx.objectStore('chatOutbox'),value=await idbRequest(store.get(sequence));
        if(!value||outboxTerminal(value)||value.state==='failed'||value.claim?.until>now)return null;
        const lane=await idbRequest(store.index('target').getAll(value.target.key));
        if(lane.some(item=>item.sequence<sequence&&!outboxTerminal(item)))return null;
        value.claim={token,until:now+lease};value.state=value.started?'uncertain':'delivering';value.revision++;store.put(value);return value;
      });
    },
    update(sequence,token,patch){return mutate(sequence,value=>{if(value.claim?.token!==token)return false;Object.assign(value,patch);return true;});},
    edit(expected,text){return mutate(expected.sequence,value=>{if(!sameSubmission(value,expected)||value.started||value.claim||value.revision!==expected.revision||!['queued','failed'].includes(value.state)||(!text.trim()&&!value.request.attachments?.length)||new TextEncoder().encode(text).byteLength>65536)return false;value.request={...value.request,text};value.state='queued';value.problem=null;return true;});},
    retry(expected){return mutate(expected.sequence,value=>{if(!sameSubmission(value,expected)||value.claim||value.state!=='failed'||value.revision!==expected.revision)return false;value.state=value.started?'uncertain':'queued';value.problem=null;return true;});},
    cancel(expected){return db.transaction(['chatOutbox','chatOutboxFiles'],'readwrite',async tx=>{
      const store=tx.objectStore('chatOutbox'),value=await idbRequest(store.get(expected.sequence));
      if(!value||!sameSubmission(value,expected)||value.started||value.claim||value.revision!==expected.revision||outboxTerminal(value))return null;
      value.state='cancelled';value.revision++;store.put(value);
      const files=tx.objectStore('chatOutboxFiles'),keys=await idbRequest(files.index('request').getAllKeys([value.target.key,value.request.request_id]));for(const key of keys)files.delete(key);return value;
    });},
    releaseFiles(expected){return db.transaction(['chatOutbox','chatOutboxFiles'],'readwrite',async tx=>{
      const entries=tx.objectStore('chatOutbox'),value=await idbRequest(entries.get(expected.sequence));
      if(!value||!sameSubmission(value,expected)||value.state!=='accepted')return null;
      const files=tx.objectStore('chatOutboxFiles'),keys=await idbRequest(files.index('request').getAllKeys([value.target.key,value.request.request_id]));for(const key of keys)files.delete(key);
      value.projection_pending=false;value.revision++;entries.put(value);return value;
    });},
    close:()=>db.close(),
  };
}
