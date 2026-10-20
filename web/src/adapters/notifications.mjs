import {claimNotificationOnboarding} from './notification-onboarding.mjs';
import {createNotificationProjection,taskOutcomeNotifications,viewingNotification,notificationText} from '../state/notifications.mjs';
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
  // Delivery currently uses the desktop Notification constructor. Mobile
  // browsers require a Service Worker delivery path; iOS also requires an
  // installed Home Screen app. Neither is implemented by this coordinator.
  const nav=host.navigator||{},ua=nav.userAgent||'';
  if(/iPhone|iPad|iPod/.test(ua)||(nav.platform==='MacIntel'&&nav.maxTouchPoints>1))return 'ios';
  if(nav.userAgentData?.mobile||/Android|Mobile/.test(ua))return 'mobile';
  if(typeof host.Notification!=='function'||typeof host.Notification.requestPermission!=='function')return 'unsupported';
  return ['default','granted','denied'].includes(host.Notification.permission)?host.Notification.permission:'unavailable';
}

export function createSystemNotifications({client,surface,view,open,locale,host=window,claim=claimBrowserNotification,onboardingClaim=claimNotificationOnboarding}) {
  const projection=createNotificationProjection(),listeners=new Set(),owners=new Map();
  let disposed=false,enabled=true,permission='loading',error='',generation=0,awaitingDirectory=true,pendingClick=null,statusEpoch=0;
  const channel=!client.desktop&&host.BroadcastChannel?new host.BroadcastChannel('pot-system-notifications-v1'):null;
  let ready=false,onboardingAttempted=false,onboarding=false,authorization=null,authorizing=false;
  const pending=new Map();
  const emit=()=>listeners.forEach(listener=>listener());
  const context=()=>({...view(),focused:host.document.visibilityState==='visible'&&host.document.hasFocus()});
  const report=()=>{if(client.desktop)client.notificationView(surface,context()).catch(()=>{});};
  const status=async()=>{
    const ticket=++statusEpoch;let nextEnabled=enabled,nextPermission,nextError='';
    try{
      if(client.desktop){const value=await client.notificationStatus();nextEnabled=value.enabled;nextPermission=value.permission;}
      else {
        try{nextEnabled=host.localStorage.getItem(preferenceKey)!=='false';}catch{}
        nextPermission=browserNotificationPermission(host);
      }
    }catch(reason){nextPermission='unavailable';nextError=reason.message;}
    if(disposed||ticket!==statusEpoch)return;
    enabled=nextEnabled;permission=nextPermission;error=nextError;
    if(!enabled||permission!=='default')onboarding=false;
    emit();maybeOnboard();
  };
  async function maybeOnboard(){
    if(!ready||disposed||onboardingAttempted||surface!=='main'||host.document.visibilityState!=='visible')return;
    if(client.desktop){
      onboardingAttempted=true;
      try{
        const result=await client.notificationReady(surface);
        // A main WebView can finish loading while its native window is hidden.
        // Revisit only on the next focus/visibility signal, never in a loop.
        if(result?.deferred){onboardingAttempted=false;return;}
        await status();
      }catch(reason){fail(reason);}
      return;
    }
    if(!enabled||authorizing||permission!=='default')return;
    onboardingAttempted=true;
    const claimed=await onboardingClaim(host).catch(()=>false);
    if(claimed&&!disposed&&enabled&&!authorizing&&browserNotificationPermission(host)==='default'){
      onboarding=true;emit();
    }
  }
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
  async function deliver(event,current=()=>!disposed) {
    if(!current())return;
    const text=notificationText(event,locale());
    if(client.desktop){await client.notifySystem({...event,...text});return;}
    const visible=await browserViewing(event);
    if(!current()||!await claim(event.id,host.indexedDB)||!current())return;
    if(visible||!enabled||browserNotificationPermission(host)!=='granted')return;
    const notification=new host.Notification(text.title,{body:text.body,tag:event.id,icon:'/assets/PotCompanion.png'});
    notification.onclick=()=>{notification.close();host.focus();Promise.resolve(open(event)).catch(fail);};
  }
  const fail=reason=>{error=reason.message||String(reason);emit();};
  const ownerKey=item=>JSON.stringify([item.control_id||item.space_id,item.space_id,item.id]);
  const bootOf=item=>(client.spaces||[]).find(space=>space.id===item.space_id)?.boot_id||'';
  function stopOwner(entry){entry.abort.abort();entry.tasks.close();entry.approvals.close();owners.delete(entry.key);}
  function watch(assistant) {
    const key=ownerKey(assistant),boot=bootOf(assistant);let entry=owners.get(key);
    if(entry&&entry.boot!==boot){stopOwner(entry);projection.reset(assistant.space_id,assistant.id,assistant.control_id);entry=null;}
    if(entry)return entry;
    entry={key,boot,space:assistant.space_id,linked:!!assistant.origin,abort:new AbortController(),cursor:null};
    const latest=()=>(client.assistants||[]).find(item=>ownerKey(item)===key);
    const current=()=>{
      const item=latest(),space=(client.spaces||[]).find(space=>space.id===entry.space);
      return !disposed&&owners.get(key)===entry&&item&&!item.offline&&space?.connected!==false&&bootOf(item)===boot;
    };
    const scope={owner:assistant.id,space:entry.space,signal:entry.abort.signal};
    const send=async events=>{for(const event of events){if(!current())return;await deliver(event,current).catch(fail);}};
    entry.tasks=createRefreshQueue(async()=>{
      let more;
      do{
        const after=entry.cursor,page=await client.request(`/api/v1/tasks/outcomes${after===null?'':`?after=${after}`}`,scope);
        if(!current())return;
        if(!Number.isSafeInteger(page.cursor)||page.cursor<0||(after!==null&&page.cursor<after)||page.has_more&&(after===null||page.cursor<=after))throw Error('Invalid notification task cursor');
        entry.cursor=page.cursor;more=page.has_more;
        if(after!==null)await send(taskOutcomeNotifications(latest(),page.items||[]));
      }while(more&&current());
    },{active:current,onError:fail});
    entry.approvals=createRefreshQueue(async()=>{
      const approvals=await client.request('/api/v1/permissions',scope);
      if(current())await send(projection.observe(latest(),(approvals.pending||[]).filter(item=>item.request?.owner===assistant.id),boot));
    },{active:current,onError:fail});
    entry.current=current;
    owners.set(key,entry);return entry;
  }
  function openPending(){
    if(!pendingClick||awaitingDirectory||disposed)return;
    const event=pendingClick;pendingClick=null;Promise.resolve(open(event)).catch(fail);
  }
  const stopClick=surface==='main'?client.onNotificationClick?.(event=>{pendingClick=event;openPending();})||(()=>{}):()=>{};
  const stopPreference=client.onNotificationPreference?.(()=>status())||(()=>{});
  status();
  return {
    get epoch(){return generation;},
    get state(){return {enabled,permission,error,onboarding,authorizing};},
    subscribe(listener){listeners.add(listener);return ()=>listeners.delete(listener);},
    async setEnabled(value){
      if(client.desktop)await client.setNotificationsEnabled(value);
      else host.localStorage.setItem(preferenceKey,String(value));
      statusEpoch++;enabled=value;if(!enabled)onboarding=false;emit();maybeOnboard();
      if(permission==='loading')await status();
    },
    ready(){if(ready)return;ready=true;maybeOnboard();},
    dismissOnboarding(){onboarding=false;emit();},
    authorize(){
      if(authorization)return authorization;
      if(!enabled)return Promise.resolve();
      // Invoke synchronously in the click's user activation, before ANY await.
      onboarding=false;error='';authorizing=true;
      let request;
      try{
        if(client.desktop)request=client.requestNotificationPermission();
        else if(browserNotificationPermission(host)==='default')request=host.Notification.requestPermission();
      }catch(reason){request=Promise.reject(reason);}
      emit();
      authorization=Promise.resolve(request).then(()=>status(),async reason=>{await status();fail(reason);throw reason;}).finally(()=>{authorization=null;authorizing=false;emit();});
      return authorization;
    },
    refresh({reset=false,space='',topics=[]}={}){
      if(reset){
        generation++;projection.reset(space);awaitingDirectory=true;
        for(const entry of owners.values())if(!space||entry.space===space)stopOwner(entry);
        return Promise.resolve();
      }
      const reads=[];
      for(const entry of owners.values())if((!space||entry.space===space)&&!entry.linked){
        if(!topics.length||topics.includes('task-outcomes'))reads.push(entry.tasks.run());
        if(!topics.length||topics.includes('permissions'))reads.push(entry.approvals.run());
      }
      return Promise.all(reads);
    },
    directory({epoch=generation}={}){
      if(epoch!==generation||disposed)return Promise.resolve();
      awaitingDirectory=false;openPending();
      const deliveries=[],rows=client.assistants||[],keys=new Set(rows.map(ownerKey));
      for(const entry of owners.values())if(!keys.has(entry.key))stopOwner(entry);
      for(const item of rows){
        const key=ownerKey(item),space=(client.spaces||[]).find(space=>space.id===item.space_id);
        if(item.offline||space?.connected===false){
          const entry=owners.get(key);if(entry)stopOwner(entry);
          projection.reset(item.space_id,item.id,item.control_id);continue;
        }
        const existing=owners.get(key),entry=watch(item);
        for(const event of projection.observe(item,undefined,entry.boot))deliveries.push(deliver(event,entry.current).catch(fail));
        if(existing!==entry&&!entry.linked)deliveries.push(entry.tasks.run(),entry.approvals.run());
      }
      return Promise.all(deliveries);
    },
    report,
    dispose(){disposed=true;for(const entry of owners.values())stopOwner(entry);channel?.close();stopClick();stopPreference();listeners.clear();host.removeEventListener('focus',focus);host.removeEventListener('blur',report);host.document.removeEventListener('visibilitychange',focus);host.removeEventListener('storage',storage);},
  };
}
