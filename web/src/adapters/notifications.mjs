import {createNotificationProjection,viewingNotification,notificationText} from '../state/notifications.mjs';
import {createRefreshQueue} from '../state/refresh.mjs';

const preferenceKey='pot-system-notifications-browser-v1';
// A read/write IndexedDB transaction serializes claims across same-origin tabs,
// including browsers without Web Locks. No server/account preference is written.
export async function claimBrowserNotification(id,indexedDB=globalThis.indexedDB) {
  return new Promise((resolve,reject)=>{
    const open=indexedDB.open('pot-system-notifications',1);
    open.onupgradeneeded=()=>open.result.createObjectStore('events');
    open.onerror=()=>reject(open.error);
    open.onsuccess=()=>{
      const db=open.result,tx=db.transaction('events','readwrite'),store=tx.objectStore('events');let claimed=false;
      const get=store.get(id);get.onsuccess=()=>{if(get.result===undefined){claimed=true;store.put(Date.now(),id);}};
      // Keep recent claims through reload/reconnect without unbounded storage.
      const cursor=store.openCursor();cursor.onsuccess=()=>{const row=cursor.result;if(!row)return;if(row.value<Date.now()-7*86400000)row.delete();row.continue();};
      tx.oncomplete=()=>{db.close();resolve(claimed);};tx.onabort=tx.onerror=()=>{db.close();reject(tx.error);};
    };
  });
}

export function browserNotificationPermission(host=globalThis) {
  if(!host.isSecureContext)return 'insecure';
  if(!host.Notification)return 'unsupported';
  return host.Notification.permission;
}

export function createSystemNotifications({client,surface,view,open,locale,host=window,claim=claimBrowserNotification}) {
  const projection=createNotificationProjection(),listeners=new Set(),abort=new AbortController();
  let disposed=false,enabled=true,permission='loading',error='',generation=0,directorySignature='',awaitingDirectory=true,pendingClick=null,statusEpoch=0;
  const channel=!client.desktop&&host.BroadcastChannel?new host.BroadcastChannel('pot-system-notifications-v1'):null;
  const pending=new Map();
  const emit=()=>listeners.forEach(listener=>listener());
  const context=()=>({...view(),focused:host.document.visibilityState==='visible'&&host.document.hasFocus()});
  const report=()=>{if(client.desktop)client.notificationView(surface,context()).catch(()=>{});};
  const status=async()=>{
    const ticket=++statusEpoch;let nextEnabled=enabled,nextPermission,nextError='';
    try{
      if(client.desktop){const value=await client.notificationStatus();nextEnabled=value.enabled;nextPermission=value.permission;}
      else {
        if(client.notificationMigrationError)throw Error(client.notificationMigrationError);
        try{nextEnabled=host.localStorage.getItem(preferenceKey)!=='false';}catch{}
        nextPermission=browserNotificationPermission(host);
      }
    }catch(reason){nextPermission='unavailable';nextError=reason.message;}
    if(disposed||ticket!==statusEpoch)return;
    enabled=nextEnabled;permission=nextPermission;error=nextError;emit();
  };
  const focus=()=>{report();status();};
  const storage=event=>{if(event.key===preferenceKey)status();};
  host.addEventListener('focus',focus);host.addEventListener('blur',report);host.document.addEventListener('visibilitychange',focus);host.addEventListener('storage',storage);
  if(channel)channel.onmessage=({data})=>{
    if(data?.type==='view-query'&&viewingNotification(context(),data.event))channel.postMessage({type:'viewing',id:data.id});
    if(data?.type==='viewing')pending.get(data.id)?.();
  };
  async function browserViewing(event) {
    if(viewingNotification(context(),event))return true;
    if(!channel)return false;
    return new Promise(resolve=>{
      const id=host.crypto.randomUUID();let visible=false;
      pending.set(id,()=>{visible=true;});channel.postMessage({type:'view-query',id,event});
      host.setTimeout(()=>{pending.delete(id);resolve(visible||viewingNotification(context(),event));},120);
    });
  }
  async function deliver(event) {
    if(disposed)return;
    const text=notificationText(event,locale());
    if(client.desktop){await client.notifySystem({...event,...text});return;}
    const ticket=generation,visible=await browserViewing(event);
    if(disposed||ticket!==generation||client.notificationMigrationError||!await claim(event.id,host.indexedDB))return;
    if(visible||!enabled||browserNotificationPermission(host)!=='granted')return;
    const notification=new host.Notification(text.title,{body:text.body,tag:event.id,icon:'/assets/PotCompanion.png'});
    notification.onclick=()=>{notification.close();host.focus();Promise.resolve(open(event)).catch(fail);};
  }
  const fail=reason=>{error=reason.message||String(reason);emit();};
  async function read() {
    if(awaitingDirectory)return;
    const ticket=generation;
    for(const assistant of client.assistants||[]) {
      if(disposed||ticket!==generation)return;
      const space=(client.spaces||[]).find(item=>item.id===assistant.space_id);
      if(assistant.offline||space?.connected===false){projection.reset(assistant.space_id);continue;}
      const scope={owner:assistant.id,space:assistant.space_id,signal:abort.signal};
      try{
        const tasks=[];let offset=0;
        do{
          const page=await client.request(`/api/v1/tasks/query?scope=all&limit=50&offset=${offset}`,scope);
          tasks.push(...(page.items||[]));
          const next=page.next_offset;if(next!=null&&next<=offset)throw Error('Invalid notification task cursor');offset=next;
        }while(offset!=null);
        const approvals=await client.request('/api/v1/permissions',scope);
        if(disposed||ticket!==generation)return;
        const latest=(client.assistants||[]).find(item=>item.id===assistant.id&&item.space_id===assistant.space_id);if(!latest)continue;
        for(const event of projection.observe(latest,tasks,(approvals.pending||[]).filter(item=>item.request?.owner===assistant.id),space?.boot_id||''))await deliver(event);
      }catch(reason){projection.reset(assistant.space_id);if(!disposed&&reason.name!=='AbortError')fail(reason);}
    }
  }
  const queue=createRefreshQueue(read,{active:()=>!disposed,onError:fail});
  function openPending(){
    if(!pendingClick||awaitingDirectory||disposed)return;
    const event=pendingClick;pendingClick=null;Promise.resolve(open(event)).catch(fail);
  }
  const stopClick=surface==='main'?client.onNotificationClick?.(event=>{pendingClick=event;openPending();})||(()=>{}):()=>{};
  const stopPreference=client.onNotificationPreference?.(()=>status())||(()=>{});
  status();
  return {
    get epoch(){return generation;},
    get state(){return {enabled,permission,error};},
    subscribe(listener){listeners.add(listener);return ()=>listeners.delete(listener);},
    async setEnabled(value){
      if(client.desktop)await client.setNotificationsEnabled(value);
      else host.localStorage.setItem(preferenceKey,String(value));
      statusEpoch++;enabled=value;emit();
      if(permission==='loading')await status();
    },
    async authorize(){
      // This method is called directly by the settings button's click handler.
      if(client.desktop)await client.requestNotificationPermission();
      else if(browserNotificationPermission(host)==='default')await host.Notification.requestPermission();
      await status();
    },
    refresh({reset=false,space=''}={}){if(reset){generation++;projection.reset(space);directorySignature='';awaitingDirectory=true;return Promise.resolve();}return queue.run();},
    directory({epoch=generation}={}){
      if(epoch!==generation||disposed)return Promise.resolve();
      awaitingDirectory=false;openPending();
      const deliveries=[],rows=client.assistants||[],signature=JSON.stringify(rows.map(item=>[item.control_id,item.space_id,item.id,item.offline,(client.spaces||[]).find(space=>space.id===item.space_id)?.boot_id]));
      for(const item of rows){
        if(item.offline){projection.reset(item.space_id);continue;}
        const boot=(client.spaces||[]).find(space=>space.id===item.space_id)?.boot_id||'';
        for(const event of projection.observe(item,undefined,undefined,boot))deliveries.push(deliver(event).catch(fail));
      }
      if(signature!==directorySignature){directorySignature=signature;deliveries.push(queue.run());}
      return Promise.all(deliveries);
    },
    report,
    dispose(){disposed=true;abort.abort();queue.close();channel?.close();stopClick();stopPreference();listeners.clear();host.removeEventListener('focus',focus);host.removeEventListener('blur',report);host.document.removeEventListener('visibilitychange',focus);host.removeEventListener('storage',storage);},
  };
}
