import {NOTIFICATION_DB_VERSION,migrateNotificationInstallation} from '../../../pkg/storage/migrations/ui-notifications/0001_installation.mjs';

export async function notificationInstallation(work,host=globalThis){
  const db=await new Promise((resolve,reject)=>{
    const request=host.indexedDB.open('pot-notification-installation',NOTIFICATION_DB_VERSION);let abandoned=false;
    request.onblocked=()=>{abandoned=true;reject(Error('notification_installation_blocked'))};
    request.onupgradeneeded=event=>{if(abandoned){request.transaction.abort();return}migrateNotificationInstallation(request.result,request.transaction,event.oldVersion)};
    request.onsuccess=()=>{if(abandoned){request.result.close();return}resolve(request.result)};request.onerror=()=>reject(request.error);
  });
  try{return await new Promise((resolve,reject)=>{
    const tx=db.transaction('installation','readwrite'),store=tx.objectStore('installation');let value,failure;
    tx.oncomplete=()=>resolve(value);tx.onabort=tx.onerror=()=>reject(failure||tx.error||Error('notification_installation_failed'));
    const get=key=>new Promise((resolve,reject)=>{const request=store.get(key);request.onsuccess=()=>resolve(request.result);request.onerror=()=>reject(request.error)});
    Promise.resolve(work({get,put:(value,key)=>store.put(value,key),remove:key=>store.delete(key)})).then(result=>value=result,error=>{failure=error;tx.abort()});
  });}finally{db.close()}
}
export function validNotificationTarget(event){
  return event&&typeof event.id==='string'&&event.id.length<=2048&&['space','control'].every(key=>typeof event[key]==='string'&&event[key].length>0&&event[key].length<=256)&&typeof event.owner==='string'&&event.owner.length>0&&event.owner.length<=512&&(!event.task||typeof event.task==='string'&&event.task.length<=256)&&['reply','task-complete','task-failed','approval'].includes(event.kind);
}
