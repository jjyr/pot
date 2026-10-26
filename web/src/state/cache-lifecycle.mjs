import {createIndexedStore} from './indexed-store.mjs';
const channelName='pot-ui-cache-lifecycle';
const clients=new Set();let channel;
export function bindCacheLifecycle(retire){
  clients.add(retire);
  if(!channel&&globalThis.BroadcastChannel){channel=new BroadcastChannel(channelName);channel.onmessage=event=>{if(event.data==='forget'){for(const close of [...clients])close();globalThis.window?.dispatchEvent(new Event('pot:cache-forgotten'));}};}
  return ()=>{clients.delete(retire);if(!clients.size){channel?.close();channel=null;}};
}
export async function clearBusinessCaches({name='pot-chat-cache',indexedDB=globalThis.indexedDB}={}){
  // Retire writers before clearing. This never touches installation credentials,
  // notification claims, preferences, or any authoritative server database.
  const signal=channel||(globalThis.BroadcastChannel?new BroadcastChannel(channelName):null);
  signal?.postMessage('forget');for(const close of [...clients])close();if(signal!==channel)signal?.close();
  for(const database of [name,name+'-tasks']){
    const store=createIndexedStore({name:database,indexedDB});
    const caches=['conversations','messages','views','cacheStats','projections','epochs','taskCheckpoints','startup'];
    try{await store.transaction(caches,'readwrite',tx=>{for(const name of caches)tx.objectStore(name).clear();});}finally{store.close();}
  }
}
