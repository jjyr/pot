import {createIndexedStore,idbRequest} from './indexed-store.mjs';

export function validStartup(value){
  return value?.schema_version===1&&value.key==='last'&&['runtime_id','boot_id','space','owner','surface'].every(key=>typeof value[key]==='string')&&!!value.runtime_id;
}
export function createStartupIdentity(options){
  const db=createIndexedStore(options);
  return {
    async read(){const value=await db.transaction(['startup'],'readonly',tx=>idbRequest(tx.objectStore('startup').get('last')));return validStartup(value)?value:null;},
    async save(runtime,scope={},surface=''){
      const value={schema_version:1,key:'last',runtime_id:runtime.runtime_id||runtime.id,boot_id:runtime.boot_id||'',space:scope.space||'',owner:scope.owner||'',surface};
      if(!validStartup(value))throw Error('Invalid startup identity');
      await db.transaction(['startup'],'readwrite',tx=>tx.objectStore('startup').put(value));return value;
    },
    async clear(){await db.transaction(['startup'],'readwrite',tx=>tx.objectStore('startup').clear());},
    close:()=>db.close(),
  };
}
