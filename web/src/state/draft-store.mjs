import {enqueueOutbox} from './chat-outbox-store.mjs';
import {createIndexedStore,idbRequest} from './indexed-store.mjs';

export function assistantDraftKey(client,scope){
  const control=(client.spaces||[]).find(space=>space.id===scope.space)?.control_id||client.runtime?.runtime_id;
  if(!control||!scope.owner)throw Error('draft_identity_missing');
  return JSON.stringify([control,scope.space||'',scope.owner]);
}

export function createDraftStore(options={}){
  const store=createIndexedStore(options),tails=new Map();
  const serial=(key,work)=>{const next=(tails.get(key)||Promise.resolve()).catch(()=>{}).then(work);tails.set(key,next);next.finally(()=>{if(tails.get(key)===next)tails.delete(key);}).catch(()=>{});return next;};
  return {
    read(key){return serial(key,()=>store.transaction(['composerDrafts','composerFiles'],'readonly',async tx=>{
      const [record,files]=await Promise.all([idbRequest(tx.objectStore('composerDrafts').get(key)),idbRequest(tx.objectStore('composerFiles').index('scope').getAll(key))]);
      return record?{record,files}:null;
    }));},
    write(key,record,files=[]){return serial(key,()=>store.transaction(['composerDrafts','composerFiles'],'readwrite',async tx=>{
      const originals=tx.objectStore('composerFiles'),existing=await idbRequest(originals.index('scope').getAllKeys(key));
      const ids=new Set(files.map(file=>file.id)),saved=new Set(existing.map(value=>value[1]));
      for(const value of existing)if(!ids.has(value[1]))originals.delete(value);
      for(const file of files)if(!saved.has(file.id))originals.put({key,id:file.id,blob:file.blob,lastModified:file.lastModified});
      if(record)tx.objectStore('composerDrafts').put({...record,key,schema_version:1});
      else tx.objectStore('composerDrafts').delete(key);
    }));},
    enqueue(key,value,files){return serial(key,()=>store.transaction(['composerDrafts','composerFiles','chatOutbox','chatOutboxFiles'],'readwrite',tx=>enqueueOutbox(tx,key,value,files)));},
    remove(key){return this.write(key,null);},
    close(){Promise.allSettled([...tails.values()]).finally(()=>store.close());},
  };
}
