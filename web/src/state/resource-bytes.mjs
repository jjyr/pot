import {createIndexedStore,idbRequest,indexedRows} from './indexed-store.mjs';
import {bindCacheLifecycle} from './cache-lifecycle.mjs';
export const resourceScope=identity=>JSON.stringify([identity.host,identity.control,identity.space,identity.owner]);
export const copyKey=(identity,kind,revision)=>JSON.stringify([resourceScope(identity),kind,revision]);
export async function digestBlob(blob){return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',await blob.arrayBuffer())),v=>v.toString(16).padStart(2,'0')).join('');}
export async function messageRevision(text){return digestBlob(new Blob([JSON.stringify(text).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'))]));}
const storageError=error=>Object.assign(error,{storage:true,uiKey:'storage.failed'});
// Immutable display copies only. A readonly hit has no write dependency. Every
// new value and notification waits for IDB transactioncomplete, including quota
// eviction. Tombstones serialize revocation with concurrent tabs' late writes.
export function createResourceCopies({name='pot-chat-cache',indexedDB=globalThis.indexedDB,maxBytes=128*1024*1024,maxEntries=512,maxScopes=1024,retention=30*86400000}={}){
  const db=createIndexedStore({name,indexedDB}),listeners=new Set();let closed=false;
  const channel=globalThis.BroadcastChannel?new BroadcastChannel('pot-resource-copies:'+name):null;
  const notify=(event,remote=false)=>{if(closed)return;for(const listener of listeners)listener(event);if(!remote)channel?.postMessage(event);};
  channel?.unref?.();
  if(channel)channel.onmessage=event=>notify(event.data,true);
  const stop=bindCacheLifecycle(()=>{notify({kind:'clear'});closed=true;db.close();channel?.close();});
  async function transaction(mode,work){if(closed)throw storageError(Error('resource_cache_closed'));try{return await db.transaction(['resourceCopies','resourceScopes','cacheStats'],mode,work);}catch(error){throw storageError(error);}}
  const valid=identity=>{if(!identity||['host','control','space','owner'].some(key=>typeof identity[key]!=='string'||!identity[key]||identity[key].length>4096)||resourceScope(identity).length>8192)throw Error('resource_cache_identity');};
  const generation=(all,row)=>String(all?.generation||0)+':'+String(row?.generation||0);
  const authority=async(tx,scope)=>{const [all,row]=await Promise.all([idbRequest(tx.objectStore('cacheStats').get('resourceAuthority')),idbRequest(tx.objectStore('resourceScopes').get(scope))]);return {all,row,generation:generation(all,row)};};
  return {
    async sources(){return transaction('readonly',async tx=>{const rows=await indexedRows(tx.objectStore('resourceCopies').index('used'),null,{limit:maxEntries+1});return [...new Map(rows.map(row=>[row.scope,row.identity])).values()];});},
    async token(identity){valid(identity);return transaction('readonly',async tx=>{const value=await authority(tx,resourceScope(identity));return {generation:value.generation,retired:!!value.row?.retired};});},
    async authorize(identity,expected){valid(identity);const scope=resourceScope(identity);await transaction('readwrite',async tx=>{const value=await authority(tx,scope);if(value.generation!==expected)throw new DOMException('Resource authorization changed','NotAllowedError');if(value.row?.retired)tx.objectStore('resourceScopes').put({...value.row,retired:false});});notify({kind:'authorized',scope});},
    async read(identity,kind,revision){valid(identity);return transaction('readonly',async tx=>{
      const scope=resourceScope(identity);if((await idbRequest(tx.objectStore('resourceScopes').get(scope)))?.retired)throw new DOMException('Resource retired','NotAllowedError');
      const row=await idbRequest(tx.objectStore('resourceCopies').get(copyKey(identity,kind,revision)));
      return row&&row.expires>Date.now()?row:null;
    });},
    async commit(identity,kind,revision,value,expected){
      valid(identity);const key=copyKey(identity,kind,revision),scope=resourceScope(identity),digest=value instanceof Blob?await digestBlob(value):null,bytes=(value instanceof Blob?value.size:new TextEncoder().encode(JSON.stringify(value)).length)+new TextEncoder().encode(key).length*2;
      if(bytes>maxBytes)throw storageError(Error('resource_cache_capacity'));
      await transaction('readwrite',async tx=>{
        const permission=await authority(tx,scope);
        if(permission.row?.retired||permission.generation!==expected)throw new DOMException('Resource retired','NotAllowedError');
        const store=tx.objectStore('resourceCopies'),previous=await idbRequest(store.get(key));
        const stats=await idbRequest(tx.objectStore('cacheStats').get('resourceCopies'))||{key:'resourceCopies',bytes:0,count:0};
        const now=Date.now();store.put({schema_version:1,key,scope,identity:{host:identity.host,control:identity.control,space:identity.space,owner:identity.owner,linked:!!identity.linked},kind,revision,value,digest,bytes,used:now,expires:now+retention});
        stats.bytes+=bytes-(previous?.bytes||0);stats.count+=previous?0:1;
        // At most the bounded catalogue; original Blobs are not copied by scans.
        const victims=await indexedRows(store.index('used'),null,{limit:maxEntries+1});
        for(const row of victims){if(row.key===key)continue;if(row.expires>now&&stats.bytes<=maxBytes&&stats.count<=maxEntries)continue;store.delete(row.key);stats.bytes-=row.bytes;stats.count--;}
        if(stats.bytes>maxBytes||stats.count>maxEntries)throw Error('resource_cache_capacity');
        tx.objectStore('cacheStats').put(stats);
      });notify({kind:'ready',key,scope});
    },
    async retire(identity){
      valid(identity);const scope=resourceScope(identity);let compacted=false,committed=false;
      try{await transaction('readwrite',async tx=>{
        const scopes=tx.objectStore('resourceScopes'),permission=await authority(tx,scope),previous=permission.row;
        // Epoch compaction bounds revocation metadata without resurrecting any
        // old read: remove all copies atomically and fence every prior writer.
        if(!previous&&await idbRequest(scopes.count())>=maxScopes){
          scopes.clear();tx.objectStore('resourceCopies').clear();
          tx.objectStore('cacheStats').put({key:'resourceAuthority',generation:(permission.all?.generation||0)+1});
          tx.objectStore('cacheStats').put({key:'resourceCopies',bytes:0,count:0});compacted=true;
        }
        scopes.put({schema_version:1,key:scope,generation:compacted?1:(previous?.generation||0)+1,retired:true});
        if(compacted)return;
        const store=tx.objectStore('resourceCopies'),rows=await indexedRows(store.index('scope'),IDBKeyRange.only(scope),{limit:maxEntries+1});
        const stats=await idbRequest(tx.objectStore('cacheStats').get('resourceCopies'))||{key:'resourceCopies',bytes:0,count:0};
        for(const row of rows){store.delete(row.key);stats.bytes-=row.bytes;stats.count--;}
        tx.objectStore('cacheStats').put(stats);
      });committed=true;}finally{if(compacted&&committed)notify({kind:'compact'});notify({kind:'retire',scope});}
    },
    subscribe(listener){listeners.add(listener);return()=>listeners.delete(listener);},
    close(){stop();closed=true;listeners.clear();channel?.close();db.close();},
  };
}
