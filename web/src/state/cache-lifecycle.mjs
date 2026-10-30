import {createIndexedStore,idbRequest} from './indexed-store.mjs';
const channelName='pot-ui-cache-lifecycle';
const clients=new Set();let channel;
export function bindCacheLifecycle(retire){
  clients.add(retire);
  if(!channel&&globalThis.BroadcastChannel){channel=new BroadcastChannel(channelName);channel.unref?.();channel.onmessage=event=>{if(event.data==='forget'){for(const close of [...clients])close();globalThis.window?.dispatchEvent(new Event('pot:cache-forgotten'));}};}
  return ()=>{clients.delete(retire);if(!clients.size){channel?.close();channel=null;}};
}
export async function clearBusinessCaches({name='pot-chat-cache',indexedDB=globalThis.indexedDB}={}){
  // Retire writers before clearing. This never touches installation credentials,
  // notification claims, preferences, or any authoritative server database.
  const signal=channel||(globalThis.BroadcastChannel?new BroadcastChannel(channelName):null);
  signal?.postMessage('forget');for(const close of [...clients])close();if(signal!==channel)signal?.close();
  for(const database of [name,name+'-tasks']){
    const store=createIndexedStore({name:database,indexedDB});
    const caches=['conversations','messages','views','cacheStats','projections','epochs','taskCheckpoints','startup','resourceCopies','resourceScopes'];
    const scopes=[...caches,'chatRecords'];
    try{await store.transaction(scopes,'readwrite',async tx=>{const previous=await idbRequest(tx.objectStore('cacheStats').get('resourceAuthority'));for(const name of caches)tx.objectStore(name).clear();
      await new Promise((resolve,reject)=>{const op=tx.objectStore('chatRecords').openCursor();op.onerror=()=>reject(op.error);op.onsuccess=()=>{const cursor=op.result;if(!cursor){resolve();return;}if(!cursor.value.local_owned){cursor.delete();}cursor.continue();};});
      tx.objectStore('cacheStats').put({key:'resourceAuthority',generation:(previous?.generation||0)+1});});}finally{store.close();}
  }
}
