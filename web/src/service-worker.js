import {notificationInstallation,validNotificationTarget} from './adapters/notification-installation.mjs';

// Only immutable UI assets are cached. API, token and remote desktop traffic
// always goes to the network and never enters the offline cache.
const CACHE='pot-ui-v4';
const ASSETS=['/index.html','/app.js','/styles.css','/assets/PotCompanion.png','/assets/IBMPlexSans-Regular.ttf'];
self.addEventListener('install',event=>event.waitUntil(caches.open(CACHE).then(cache=>cache.addAll(ASSETS)).then(()=>self.skipWaiting())));
self.addEventListener('activate',event=>event.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(key=>key.startsWith('pot-ui-')&&key!==CACHE).map(key=>caches.delete(key)))).then(()=>self.clients.claim())));
self.addEventListener('fetch',event=>{
 const request=event.request,url=new URL(request.url);
 if(request.method!=='GET'||url.origin!==self.location.origin||url.pathname.startsWith('/api/')||url.pathname.startsWith('/access/')||url.pathname.startsWith('/wails/'))return;
 if(request.mode==='navigate'){event.respondWith(fetch(request).catch(()=>caches.open(CACHE).then(cache=>cache.match('/index.html'))));return;}
 if(!/\.(?:m?js|css|png|ttf|webmanifest)$/.test(url.pathname))return;
 event.respondWith(fetch(request).then(response=>{if(response.ok){const clone=response.clone();caches.open(CACHE).then(cache=>cache.put(request,clone));}return response;}).catch(()=>caches.open(CACHE).then(cache=>cache.match(request))));
});

// Push carries one already-selected event. Policy, leases and deduplication stay
// in the backend dispatcher; the worker performs only device fencing/display.
self.addEventListener('push',event=>event.waitUntil((async()=>{
  let payload;try{payload=event.data?.json()}catch{return}
  if(payload?.version!==1||!validNotificationTarget(payload.event)||typeof payload.title!=='string'||typeof payload.body!=='string'||payload.title.length>256||payload.body.length>512)return;
  const device=await notificationInstallation(store=>store.get('device'));
  if(!device?.enabled||device.channel!=='push'||device.id!==payload.device||device.control!==payload.event.control||payload.event.space!==device.control)return;
  await self.registration.showNotification(payload.title,{body:payload.body,tag:payload.event.id,icon:'/assets/PotCompanion.png',data:{device:device.id,event:payload.event}});
})()));
self.addEventListener('notificationclick',event=>{
  event.notification.close();event.waitUntil((async()=>{
    const data=event.notification.data;if(!validNotificationTarget(data?.event))return;
    const accepted=await notificationInstallation(async store=>{
      const device=await store.get('device');if(!device?.enabled||device.id!==data.device||device.control!==data.event.control)return false;
      store.put({schema_version:1,device:device.id,event:data.event},'click');return true;
    });
    if(!accepted)return;
    const windows=await self.clients.matchAll({type:'window',includeUncontrolled:true});
    const existing=windows.find(client=>new URL(client.url).origin===self.location.origin&&new URL(client.url).pathname==='/');
    if(existing){await existing.focus();existing.postMessage({type:'pot:notification-click'})}
    else await self.clients.openWindow('/');
  })());
});
