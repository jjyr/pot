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
  const check=options=>{if(options?.signal?.aborted||options?.valid&&!options.valid())throw new DOMException('Notification operation retired','AbortError')};
  async function current(store,options){check(options);const actual=await store.get('epoch');check(options);if((actual||0)!==epoch)throw Error('notification_session_retired')}
  async function expired(error,options){
    check(options);if(error.status!==410)return false;
    const previous=subscription;subscription=null;registeredSignature=null;
    await notificationInstallation(async store=>{await current(store,options);const value=await store.get('device');check(options);if(value?.id===device?.id)store.put({...value,enabled:false},'device')},host);
    check(options);try{await previous?.unsubscribe()}catch{}check(options);return true;
  }
  return {
    get device(){return device},
    get subscribed(){return !!subscription},
    async initialize(channel,enabled,options={}){
      check(options);epoch=await notificationInstallation(store=>store.get('epoch'),host)||0;check(options);
      const session=await request('/api/v1/notifications/session',undefined,options.signal);check(options);
      const value=await notificationInstallation(async store=>{
        await current(store,options);const previous=await store.get('device');check(options);
        const next=previous?.binding===session.binding&&previous.control===session.control?previous:{schema_version:1,id:host.crypto.randomUUID(),control:session.control,binding:session.binding};
        const value={...next,channel,enabled},click=await store.get('click');check(options);
        if(click&&previous&&click.device===previous.id&&click.event?.control===value.control)store.put({...click,device:value.id},'click');store.put(value,'device');return value;
      },host);check(options);device=value;
      if(channel==='push'){
        const nextKey=await request('/api/v1/notifications/key',undefined,options.signal);check(options);
        const reg=await registration();check(options);const existing=await reg.pushManager.getSubscription();check(options);
        key=nextKey;subscription=existing&&sameServerKey(existing,key,host)?existing:null;
      }
      return device;
    },
    // Permission is invoked synchronously by the caller's explicit action.
    async subscribe(options={}){
      await notificationInstallation(store=>current(store,options),host);check(options);
      const reg=await registration();check(options);let next=await reg.pushManager.getSubscription();check(options);
      if(next&&!sameServerKey(next,key,host)){await next.unsubscribe();check(options);next=null}
      if(!next){next=await reg.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:applicationServerKey(key.public_key,host)});check(options)}
      subscription=next;return subscription;
    },
    async intent(enabled,options={}){
      check(options);await notificationInstallation(async store=>{
        const value=await store.get('device');check(options);if(value)store.put({...value,enabled},'device');
      },host);check(options);
    },
    async register(channel,enabled,permission,options={}){
      check(options);if(!device)return;if(channel==='push'&&!subscription)return false;
      const body={device:device.id,control:device.control,space:device.control,channel,enabled,permission,locale:locale(),...(channel==='push'?{subscription:subscription.toJSON()}:{})};
      const signature=JSON.stringify(body);if(signature===registeredSignature)return true;
      await notificationInstallation(async store=>{await current(store,options);const value=await store.get('device');check(options);if(value?.id===device.id)store.put({...value,enabled,channel},'device')},host);check(options);
      // A sent registration may commit before its response arrives. Explicit
      // off must remove it even when a prior no-subscription setup was disabled.
      disabled=false;
      try{await request('/api/v1/notifications/device',body,options.signal);check(options);registeredSignature=signature;return true}catch(error){if(await expired(error,options))return false;throw error}
    },
    async disable(options={}){
      check(options);if(disabled)return;registeredSignature=null;
      await this.intent(false,options);check(options);
      if(device)await request('/api/v1/notifications/remove',{device:device.id},options.signal);check(options);disabled=true;
      // The inert browser subscription can be reused by an explicit re-enable.
    },
    async lease(session,view,options={}){check(options);if(!device)return;try{await request('/api/v1/notifications/view',{device:device.id,session,control:device.control,view},options.signal);check(options);return true}catch(error){if(await expired(error,options))return false;throw error}},
    baseline(session,options={}){check(options);if(!device)return Promise.resolve();return request('/api/v1/notifications/baseline',{device:device.id,session},options.signal)},
    next(signal,session){return request('/api/v1/notifications/next',{device:device.id,session},signal)},
    receipt(delivery,state,options={}){check(options);return request('/api/v1/notifications/receipt',{device:device.id,event:delivery.event.id,attempt:delivery.attempt,state},options.signal)},
  };
}
export async function disableNotificationInstallation(host=globalThis){
  await notificationInstallation(async store=>{store.put((await store.get('epoch')||0)+1,'epoch');const value=await store.get('device');if(value)store.put({...value,enabled:false},'device');store.remove('click')},host);
}
