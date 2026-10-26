import {notificationInstallation} from './notification-installation.mjs';

// API support is independent of permission and the preference. Installed desktop
// PWAs retain the constructor channel. iOS exposes Notification but only permits
// Service Worker notifications in an installed Home Screen application.
export function notificationCapabilities(host=globalThis){
  if(!host.isSecureContext)return {channel:null,limitation:'insecure'};
  const nav=host.navigator||{},ua=nav.userAgent||'';
  const ios=/iPhone|iPad|iPod/.test(ua)||(nav.platform==='MacIntel'&&nav.maxTouchPoints>1);
  const constructorUnavailable=ios||/Android/.test(ua);
  const notification=typeof host.Notification==='function'&&typeof host.Notification.requestPermission==='function';
  if(notification&&!constructorUnavailable)return {channel:'local'};
  const installed=nav.standalone===true||host.matchMedia?.('(display-mode: standalone)').matches;
  if(ios&&!installed)return {channel:null,limitation:'iosInstall'};
  if(notification&&nav.serviceWorker&&typeof host.PushManager==='function'&&typeof host.ServiceWorkerRegistration?.prototype?.showNotification==='function')return {channel:'push'};
  return {channel:null,limitation:ios?'iosUnsupported':'unsupported'};
}
export function notificationPermission(host=globalThis){
  const capability=notificationCapabilities(host);if(!capability.channel)return capability.limitation;
  return ['default','granted','denied'].includes(host.Notification.permission)?host.Notification.permission:'unavailable';
}
function applicationServerKey(value,host){return Uint8Array.from(host.atob(value.replace(/-/g,'+').replace(/_/g,'/')),char=>char.charCodeAt(0))}
function sameServerKey(subscription,key,host){const expected=applicationServerKey(key.public_key,host),actual=new Uint8Array(subscription?.options?.applicationServerKey||[]);return actual.length===expected.length&&expected.every((byte,index)=>byte===actual[index])}
export function createPushInstallation({client,host=globalThis,locale}){
  const request=(path,body,signal)=>client.transport(path,{owner:'',space:'',...(body===undefined?{}:{method:'POST',body}),signal});
  let device=null,subscription=null,key=null,epoch=null,registeredSignature=null,disabled=false;
  const registration=()=>host.navigator.serviceWorker.ready;
  async function expired(error){if(error.status!==410)return false;const previous=subscription;subscription=null;registeredSignature=null;await notificationInstallation(async store=>{const value=await store.get('device');if(value?.id===device?.id)store.put({...value,enabled:false},'device')},host);try{await previous?.unsubscribe()}catch{}return true}
  return {
    get device(){return device},
 get subscribed(){return !!subscription},
    async initialize(channel,enabled){
      epoch=await notificationInstallation(store=>store.get('epoch'),host)||0;
      const session=await request('/api/v1/notifications/session');
      device=await notificationInstallation(async store=>{
        if((await store.get('epoch')||0)!==epoch)throw Error('notification_session_retired');
        const previous=await store.get('device');
        const next=previous?.binding===session.binding&&previous.control===session.control?previous:{schema_version:1,id:host.crypto.randomUUID(),control:session.control,binding:session.binding};
        const value={...next,channel,enabled};const click=await store.get('click');if(click&&previous&&click.device===previous.id&&click.event?.control===value.control)store.put({...click,device:value.id},'click');store.put(value,'device');return value;
      },host);
      if(channel==='push'){key=await request('/api/v1/notifications/key');const existing=await (await registration()).pushManager.getSubscription();subscription=existing&&sameServerKey(existing,key,host)?existing:null}
      return device;
    },
    // Called only by the explicit authorization action. Permission invocation is
    // synchronous in the caller; subscribe follows that same user gesture flow.
    async subscribe(){
      if((await notificationInstallation(store=>store.get('epoch'),host)||0)!==epoch)throw Error('notification_session_retired');
      const reg=await registration();subscription=await reg.pushManager.getSubscription();
      if(subscription&&!sameServerKey(subscription,key,host)){await subscription.unsubscribe();subscription=null}
      if(!subscription){subscription=await reg.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:applicationServerKey(key.public_key,host)})}
      return subscription;
    },
    async register(channel,enabled,permission){
      if(!device)return;
      if(channel==='push'&&!subscription)return false;
      const body={device:device.id,control:device.control,space:device.control,channel,enabled,permission,locale:locale(),...(channel==='push'?{subscription:subscription.toJSON()}:{} )};
      const signature=JSON.stringify(body);if(signature===registeredSignature)return true;
      await notificationInstallation(async store=>{if((await store.get('epoch')||0)!==epoch)throw Error('notification_session_retired');const value=await store.get('device');if(value?.id===device.id)store.put({...value,enabled,channel},'device')},host);
      try{await request('/api/v1/notifications/device',body);registeredSignature=signature;disabled=false;return true}catch(error){if(await expired(error))return false;throw error}
    },
    async disable(){
      if(disabled)return;registeredSignature=null;
      if(device)await notificationInstallation(async store=>{const value=await store.get('device');if(value?.id===device.id)store.put({...value,enabled:false},'device')},host);
      if(device)await request('/api/v1/notifications/remove',{device:device.id});disabled=true;
      // Keep the browser subscription for explicit re-enable without re-prompting;
      // the server registration is gone and the worker checks local intent.
    },
    async lease(session,view){if(!device)return;try{await request('/api/v1/notifications/view',{device:device.id,session,control:device.control,view});return true}catch(error){if(await expired(error))return false;throw error}},
    baseline(session){if(!device)return Promise.resolve();return request('/api/v1/notifications/baseline',{device:device.id,session})},
    next(signal,session){return request('/api/v1/notifications/next',{device:device.id,session},signal)},
    receipt(delivery,state){return request('/api/v1/notifications/receipt',{device:device.id,event:delivery.event.id,attempt:delivery.attempt,state})},
  };
}
export async function disableNotificationInstallation(host=globalThis){
  await notificationInstallation(async store=>{store.put((await store.get('epoch')||0)+1,'epoch');const value=await store.get('device');if(value)store.put({...value,enabled:false},'device');store.remove('click')},host);
}
