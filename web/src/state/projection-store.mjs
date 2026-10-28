import {bindCacheLifecycle} from './cache-lifecycle.mjs';
import {createIndexedStore,idbRequest,indexedRows} from './indexed-store.mjs';
const namespace=meta=>JSON.stringify([meta.control,meta.space]);
const failure=error=>Object.assign(error,{storage:true,uiKey:'storage.failed'});
const token=(epoch,topics)=>JSON.stringify([epoch?.all||0,...topics.map(topic=>epoch?.[topic]||0)]);
const estimate=value=>new TextEncoder().encode(JSON.stringify(value)).length;
const isCommand=meta=>typeof meta?.path==='string'&&meta.path.startsWith('@command:');
export const projectionKey=meta=>isCommand(meta)?JSON.stringify(meta):JSON.stringify({...meta,boot:undefined});

// Display snapshots and domain invalidation receipts share one transaction.
// Tokens fence HTTP reads against events/mutations in this or another tab.
export function createProjectionStore({name='pot-chat-cache',indexedDB=globalThis.indexedDB,maxEntries=256,maxBytes=64*1024*1024,retention=7*86400000}={}){
  const db=createIndexedStore({name,indexedDB}),listeners=new Set();let closed=false;
  const stopLifecycle=bindCacheLifecycle(()=>{closed=true;db.close();});
  const channel=globalThis.BroadcastChannel?new BroadcastChannel('pot-projection-commits:'+name):null;
  function notify(event,remote=false){if(closed)return;for(const listener of listeners)listener(event,{remote});if(!remote)channel?.postMessage(event);}
  if(channel)channel.onmessage=event=>notify(event.data,true);
  async function transaction(mode,work){try{return await db.transaction(['projections','epochs','cacheStats'],mode,work);}catch(error){throw failure(error);}}
  const epoch=(tx,meta)=>idbRequest(tx.objectStore('epochs').get(namespace(meta)));
  return {
    async token(meta){return transaction('readonly',async tx=>token(await epoch(tx,meta),meta.topics));},
    async read(key){return transaction('readonly',async tx=>{
      const row=await idbRequest(tx.objectStore('projections').get(key));if(!row||row.expires<Date.now())return null;
      return {...row,fresh:row.token===token(await epoch(tx,row.meta),row.meta.topics)};
    });},
    async commit(key,meta,value,expectedToken,validate=null){
      const result=await transaction('readwrite',async tx=>{
        const current=token(await epoch(tx,meta),meta.topics);if(current!==expectedToken)return false;
        const store=tx.objectStore('projections'),previous=await idbRequest(store.get(key)),stats=await idbRequest(tx.objectStore('cacheStats').get('projections'))||{key:'projections',bytes:0,count:0};
        if(validate)validate();
        const row={schema_version:isCommand(meta)?1:2,key,meta,namespace:namespace(meta),value,token:current,lastConfirmed:Date.now(),used:Date.now(),expires:Date.now()+retention,bytes:estimate(value)};
        if(row.bytes>maxBytes)throw Error('projection_too_large');
        stats.bytes+=row.bytes-(previous?.bytes||0);stats.count+=previous?0:1;store.put(row);
        if(stats.count>maxEntries||stats.bytes>maxBytes){
          const victims=await indexedRows(store.index('used'),null,{limit:maxEntries+1});
          for(const victim of victims){if(stats.count<=maxEntries&&stats.bytes<=maxBytes)break;if(victim.key===key)continue;store.delete(victim.key);stats.count--;stats.bytes-=victim.bytes;}
        }
        tx.objectStore('cacheStats').put(stats);
        if(validate)validate();
        return true;
      });
      if(result)notify({kind:'commit',key,meta});return result;
    },
    async command(key,meta,value){
      await transaction('readwrite',async tx=>{
        const store=tx.objectStore('projections');if(await idbRequest(store.get(key)))return;
        const previous=await epoch(tx,meta)||{key:namespace(meta),space:meta.space,all:0},next={...previous};for(const topic of meta.topics)next[topic]=(next[topic]||0)+1;tx.objectStore('epochs').put(next);
        const row={schema_version:1,key,meta,namespace:namespace(meta),value,token:token(next,meta.topics),lastConfirmed:Date.now(),used:Date.now(),expires:Date.now()+86400000,bytes:estimate(value)};
        if(row.bytes>maxBytes)throw Error('projection_too_large');store.put(row);
        const stats=await idbRequest(tx.objectStore('cacheStats').get('projections'))||{key:'projections',bytes:0,count:0};stats.count++;stats.bytes+=row.bytes;
        if(stats.count>maxEntries||stats.bytes>maxBytes){const victims=await indexedRows(store.index('used'),null,{limit:maxEntries+1});for(const victim of victims){if(stats.count<=maxEntries&&stats.bytes<=maxBytes)break;if(victim.key===key)continue;store.delete(victim.key);stats.count--;stats.bytes-=victim.bytes;}}
        tx.objectStore('cacheStats').put(stats);
      });
      notify({kind:'invalidate',topics:meta.topics,source:{control:meta.control,space:meta.space}});
      return (await this.read(key)).value;
    },
    async invalidate(topics,source={}){
      // Epochs contain small domain counters, never cached response bodies.
      await transaction('readwrite',async tx=>{
        const store=tx.objectStore('epochs'),rows=await idbRequest(store.getAll());
        const keys=new Set(rows.filter(row=>!source.space||row.space===source.space).map(row=>row.key));
        if(source.control)keys.add(namespace(source));
        for(const key of keys){const previous=rows.find(row=>row.key===key)||{key,space:source.space||''},next={...previous};if(!topics.length)next.all=(next.all||0)+1;for(const topic of topics)next[topic]=(next[topic]||0)+1;store.put(next);}
      });notify({kind:'invalidate',topics,source});
    },
    async register(meta){await transaction('readwrite',async tx=>{if(!await epoch(tx,meta))tx.objectStore('epochs').put({key:namespace(meta),space:meta.space,all:0});});},
    async remove(key){await transaction('readwrite',async tx=>{const store=tx.objectStore('projections'),row=await idbRequest(store.get(key));if(!row)return;const stats=await idbRequest(tx.objectStore('cacheStats').get('projections'));store.delete(key);if(stats)tx.objectStore('cacheStats').put({...stats,count:Math.max(0,stats.count-1),bytes:Math.max(0,stats.bytes-row.bytes)});});notify({kind:'remove',key});},
    subscribe(listener){listeners.add(listener);return ()=>listeners.delete(listener);},
    close(){stopLifecycle();closed=true;channel?.close();listeners.clear();db.close();},
  };
}

// Explicit nonpersistent backend for isolated transport/unit fixtures. Production
// never selects it implicitly when IndexedDB fails.
export function createMemoryProjectionStore(){
  const rows=new Map(),epochs=new Map(),listeners=new Set();
  return {
    async register(){},async token(meta){return token(epochs.get(namespace(meta)),meta.topics);},
    async read(key){const row=rows.get(key);return row?{...structuredClone(row),fresh:row.token===token(epochs.get(namespace(row.meta)),row.meta.topics)}:null;},
    async commit(key,meta,value,expected,validate=null){if(expected!==await this.token(meta))return false;if(validate)validate();const next={schema_version:isCommand(meta)?1:2,key,meta,value:structuredClone(value),token:expected,lastConfirmed:Date.now()};rows.set(key,next);for(const listener of listeners)listener({kind:'commit',key,meta},{remote:false});return true;},
    async invalidate(topics,source={}){for(const row of rows.values()){if(source.space&&row.meta.space!==source.space)continue;const key=namespace(row.meta),previous=epochs.get(key)||{};if(!topics.length)previous.all=(previous.all||0)+1;for(const topic of topics)previous[topic]=(previous[topic]||0)+1;epochs.set(key,previous);}},
    async command(key,meta,value){await this.invalidate(meta.topics,meta);await this.commit(key,meta,value,await this.token(meta));return (await this.read(key)).value;},
    async remove(key){rows.delete(key);},subscribe(listener){listeners.add(listener);return ()=>listeners.delete(listener);},close(){listeners.clear();},
  };
}
