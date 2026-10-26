import {assistantDraftKey,createDraftStore} from './draft-store.mjs';
import {draft,draftReferences,ownerKey} from './conversation.mjs';

// One writer per assistant in this client. A retired conversation/load cannot
// publish into its successor; each drain captures the newest in-memory revision.
export function createAssistantDrafts({client,state,attachments,changed,restored,restorePending,storage=createDraftStore({name:client.chatCacheName||'pot-chat-cache'}),clock=globalThis,delay=300}){
  const entries=new Map(),bindings=new Map(),deletions=new Map();let closed=false;
  const current=entry=>!closed&&!entry.retired&&entries.get(entry.key)===entry;
  const notify=()=>{if(!closed)changed();};
  function snapshot(entry){
    const key=entry.stateKey,items=attachments.items(key),pending=state.pending.get(key);
    return {record:{conversation_id:entry.conversation,state_key:key,text:draft(state,key),references:draftReferences(state,key),draftVersion:state.draftVersions.get(key)||0,referenceVersion:state.referenceVersions.get(key)||0,
      attachments:items.map(item=>({attachment:{...item.attachment},phase:item.phase==='uploading'?'waiting':item.phase,error:item.error||'',submitted:!!item.submitted})),
      pending:pending?{request:structuredClone(pending.request),key,draftVersion:pending.draftVersion,referenceVersion:pending.referenceVersion}:null},
      files:items.filter(item=>item.file).map(item=>({id:item.attachment.id,blob:item.file,lastModified:item.file.lastModified}))};
  }
  async function drain(entry){
    clock.clearTimeout(entry.timer);entry.timer=undefined;
    await entry.loading;
    if(!current(entry))return;
    if(!entry.loaded)throw entry.error;
    if(entry.flight)return entry.flight;
    entry.flight=(async()=>{
      while(current(entry)&&entry.saved<entry.revision){
        const revision=entry.revision,{record,files}=snapshot(entry);
        try{await storage.write(entry.key,record,files);entry.saved=revision;entry.error=null;}
        catch(error){entry.error=error;throw error;}
      }
    })().finally(()=>{entry.flight=null;notify();});
    return entry.flight;
  }
  function save(entry,immediate=false){
    if(!current(entry)||entry.restoring)return;
    entry.revision++;clock.clearTimeout(entry.timer);
    if(immediate)drain(entry).catch(()=>{});
    else entry.timer=clock.setTimeout(()=>drain(entry).catch(()=>{}),delay);
  }
  function load(entry){
    const key=entry.stateKey,textVersion=entry.initialTextVersion,refVersion=entry.initialRefVersion;
    entry.loading=storage.read(entry.key).then(saved=>{
      if(!current(entry))return;
      entry.restoring=true;
      try{
        const value=saved?.record;
        if(value&&value.conversation_id===entry.conversation){
          if((state.draftVersions.get(key)||0)===textVersion){state.drafts.set(key,value.text);state.draftVersions.set(key,value.draftVersion);}
          else state.draftVersions.set(key,Math.max(state.draftVersions.get(key)||0,value.draftVersion)+1);
          if((state.referenceVersions.get(key)||0)===refVersion){state.referenceDrafts.set(key,value.references);state.referenceVersions.set(key,value.referenceVersion);}
          else state.referenceVersions.set(key,Math.max(state.referenceVersions.get(key)||0,value.referenceVersion)+1);
          attachments.hydrate(key,value.attachments,saved.files,entry.scope);
          if(value.pending){
            const receipt={...value.pending,key,epoch:state.epoch,phase:'uncertain',recoverable:true,restored:true};
            state.pending.set(key,receipt);restorePending(receipt,entry.scope);
          }
        }else if(value){entry.revision++;}
        entry.loaded=true;entry.error=null;
      }finally{entry.restoring=false;}
      restored(entry.stateKey);notify();
    }).catch(error=>{if(current(entry)){entry.error=error;notify();}}).finally(()=>{entry.loading=null;if(current(entry)&&entry.loaded&&entry.revision>entry.saved)save(entry,true);});
    return entry.loading;
  }
  return {
    activate(snapshot,scope){
      const key=assistantDraftKey(client,scope),stateKey=ownerKey(snapshot);let entry=entries.get(key);
      if(entry&&entry.stateKey!==stateKey){
        entry.retired=true;clock.clearTimeout(entry.timer);bindings.delete(entry.stateKey);
        attachments.discard(entry.stateKey);
        for(const map of [state.drafts,state.referenceDrafts,state.pending,state.retries,state.sends])map.delete(entry.stateKey);
        entry=null;
      }
      if(!entry){entry={key,stateKey,scope:{...scope},conversation:snapshot.conversation_id,revision:0,saved:0,loaded:false,error:null,initialTextVersion:state.draftVersions.get(stateKey)||0,initialRefVersion:state.referenceVersions.get(stateKey)||0};entries.set(key,entry);bindings.set(stateKey,entry);load(entry);}
      return entry;
    },
    change(key,immediate=false){const entry=bindings.get(key);if(entry)save(entry,immediate);},
    ready(key){return !!bindings.get(key)?.loaded;},
    status(key){const cleanup=[...deletions.values()].find(value=>value.error);if(cleanup)return {error:cleanup.error,cleanup:true};const entry=bindings.get(key);return entry?{loading:!entry.loaded,error:entry.error,dirty:entry.saved<entry.revision,saving:!!entry.flight}:null;},
    async flush(key){const entry=bindings.get(key);if(entry)await drain(entry);},
    async retry(key){for(const [target,value]of deletions){try{await storage.remove(target);deletions.delete(target);}catch(error){value.error=error;notify();throw error;}}const entry=bindings.get(key);if(!entry){notify();return;}if(!entry.loaded)await load(entry);await drain(entry);},
    async forget(scope){
      const key=assistantDraftKey(client,scope),entry=entries.get(key);
      if(entry){
        entry.retired=true;clock.clearTimeout(entry.timer);bindings.delete(entry.stateKey);entries.delete(key);attachments.discard(entry.stateKey);
        for(const map of [state.drafts,state.draftVersions,state.referenceDrafts,state.referenceVersions,state.pending,state.retries,state.sends])map.delete(entry.stateKey);
      }
      // Store serialization puts deletion after all already-issued writes.
      const deletion={};deletions.set(key,deletion);
      try{await storage.remove(key);deletions.delete(key);}catch(error){deletion.error=error;notify();throw error;}
    },
    close(){
      // Capture before the DOM owner disposes Files and object URLs. Already
      // issued writes precede these final snapshots in the store's key queue.
      const writes=[];
      for(const entry of entries.values()){
        clock.clearTimeout(entry.timer);entry.retired=true;
        if(entry.loaded&&entry.saved<entry.revision){const {record,files}=snapshot(entry);writes.push(storage.write(entry.key,record,files));}
      }
      closed=true;Promise.allSettled(writes).finally(()=>storage.close());
    },
  };
}
