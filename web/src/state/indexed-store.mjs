import {UI_CACHE_VERSION,migrateStableProjections} from '../../../pkg/storage/migrations/ui-cache/0009_stable_projections.mjs';

export const idbRequest=value=>new Promise((resolve,reject)=>{value.onsuccess=()=>resolve(value.result);value.onerror=()=>reject(value.error);});
// Resolves work only at transaction completion. Never expose a put's success as
// a committed projection. Each caller declares exactly the stores it touches.
export function createIndexedStore({indexedDB=globalThis.indexedDB,name='pot-chat-cache',version=UI_CACHE_VERSION,migrate=migrateStableProjections}={}){
  let opening,closed=false;
  function open(){
    if(closed)return Promise.reject(Error('chat_cache_closed'));
    if(!opening)opening=new Promise((resolve,reject)=>{
      if(!indexedDB){reject(Error('chat_cache_unavailable'));return;}
      const request=indexedDB.open(name,version);let abandoned=false,upgradeError;
      request.onupgradeneeded=event=>{if(abandoned||closed){request.transaction.abort();return;}try{migrate(request.result,request.transaction,event.oldVersion);}catch(error){upgradeError=error;request.transaction.abort();}};
      request.onsuccess=()=>{const db=request.result;if(abandoned||closed){db.close();return;}db.onversionchange=()=>{db.close();opening=null;};resolve(db);};
      request.onerror=()=>reject(upgradeError||request.error);
      request.onblocked=()=>{abandoned=true;reject(Error('chat_cache_blocked'));};
    }).catch(error=>{opening=null;throw error;});
    return opening;
  }
  return {
    async transaction(stores,mode,work){const db=await open();return new Promise((resolve,reject)=>{
      const tx=db.transaction(stores,mode);let result,failure;
      tx.oncomplete=()=>resolve(result);
      tx.onabort=tx.onerror=()=>reject(failure||tx.error||Error('chat_cache_transaction'));
      Promise.resolve().then(()=>work(tx)).then(value=>{result=value;},error=>{failure=error;try{tx.abort();}catch{reject(error);}});
    });},
    close(){closed=true;opening?.then(db=>db.close()).catch(()=>{});},
  };
}

export function indexedRows(index,range,{direction='next',limit=80,accept=()=>true}={}){
  return new Promise((resolve,reject)=>{const result=[],request=index.openCursor(range,direction);request.onerror=()=>reject(request.error);request.onsuccess=()=>{const cursor=request.result;if(!cursor){resolve(result);return;}if(accept(cursor.value))result.push(cursor.value);if(result.length>=limit)resolve(result);else cursor.continue();};});
}
