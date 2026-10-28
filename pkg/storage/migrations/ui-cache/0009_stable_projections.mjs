import {migrateResourceBytes} from './0008_resource_bytes.mjs';

export const UI_CACHE_VERSION=9;

// Stable projections are keyed by display identity without boot. A boot change
// can no longer evict committed business views; volatile memory still fences
// in-flight IO against the current boot. Non-command rows are re-keyed here.
const stableKey=meta=>JSON.stringify({...meta,boot:undefined});
const isCommand=meta=>typeof meta?.path==='string'&&meta.path.startsWith('@command:');

export function migrateStableProjections(db,tx,oldVersion){
  migrateResourceBytes(db,tx,oldVersion);
  if(oldVersion>=UI_CACHE_VERSION)return;
  if(!db.objectStoreNames.contains('projections'))return;
  const store=tx.objectStore('projections');
  const rows=[];
  const request=store.openCursor();
  // After enumeration, choose winners and rewrite. Cursor iteration has already
  // completed by the time the final onsuccess fires, so queue mutations then.
  request.onsuccess=()=>{
    const cursor=request.result;
    if(cursor){
      rows.push(cursor.value);
      cursor.continue();
      return;
    }
    const byKey=new Map();
    for(const row of rows){
      if(!row||!row.meta)continue;
      if(isCommand(row.meta))continue;
      const key=stableKey(row.meta);
      const previous=byKey.get(key);
      if(!previous){
        byKey.set(key,{row,key});
        continue;
      }
      const nextTime=row.lastConfirmed??-Infinity;
      const previousTime=previous.row.lastConfirmed??-Infinity;
      const winner=nextTime>previousTime||(nextTime===previousTime&&String(row.key)<String(previous.row.key))?{row,key}:previous;
      byKey.set(key,winner);
    }
    const keptKeys=new Set();
    let bytes=0,count=0;
    for(const {row,key} of byKey.values()){
      const updated={...row,schema_version:2,key};
      keptKeys.add(key);
      bytes+=updated.bytes||0;
      count++;
      store.put(updated);
    }
    for(const row of rows){
      if(!row||!row.meta)continue;
      if(isCommand(row.meta)){
        if(!keptKeys.has(row.key)){
          bytes+=row.bytes||0;
          count++;
          keptKeys.add(row.key);
        }
        continue;
      }
      const key=stableKey(row.meta);
      if(!keptKeys.has(key)&&!keptKeys.has(row.key))store.delete(row.key);
      else if(keptKeys.has(key)&&key!==row.key)store.delete(row.key);
    }
    const statsStore=tx.objectStore('cacheStats');
    const got=statsStore.get('projections');
    got.onsuccess=()=>{
      statsStore.put({...(got.result||{}),key:'projections',bytes,count});
    };
  };
}
