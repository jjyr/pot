import {notificationInstallation,validNotificationTarget} from './adapters/notification-installation.mjs';

// Injected from the complete embedded resource tree by webassets.FS(). A failed
// install never activates; the installed version keeps serving its own resources.
const SHELL=/* POT_SHELL_MANIFEST */ null;
const CACHE='pot-ui-shell-'+SHELL.version;
const paths=new Map(SHELL.assets.map(asset=>[asset.url,asset]));
async function verified(response,asset){
 if(!response?.ok||response.redirected)return false;
 const bytes=await response.clone().arrayBuffer();
 const hash=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),byte=>byte.toString(16).padStart(2,'0')).join('');
 return hash===asset.sha256;
}
async function download(asset){
 const response=await fetch(asset.url,{cache:'no-store',credentials:'same-origin'});
 if(!await verified(response,asset))throw Error('Incomplete or mismatched Pot shell');
 return response;
}
self.addEventListener('install',event=>event.waitUntil((async()=>{
 const cache=await caches.open(CACHE);
 try{
  // Bounded IO, integrity checked before the browser may activate this worker.
  for(let offset=0;offset<SHELL.assets.length;offset+=8)await Promise.all(SHELL.assets.slice(offset,offset+8).map(async asset=>{
   const response=await download(asset);
   await cache.put(asset.url,response);
  }));
 }catch(error){await caches.delete(CACHE);throw error;}
 // No skipWaiting: existing documents keep their complete installed version.
})()));
self.addEventListener('activate',event=>event.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(key=>key.startsWith('pot-ui-')&&key!==CACHE).map(key=>caches.delete(key))))));
self.addEventListener('fetch',event=>{
 const request=event.request,url=new URL(request.url);
 if(request.method!=='GET'||url.origin!==self.location.origin||url.pathname.startsWith('/api/')||url.pathname.startsWith('/access/')||url.pathname.startsWith('/wails/'))return;
 const path=request.mode==='navigate'&&url.pathname!=='/resource-document.html'?'/index.html':url.pathname;
 if(!paths.has(path))return;
 // Repair an evicted member only from the exact installed version. Never
 // substitute another release's file, even when that server is reachable.
 event.respondWith(caches.open(CACHE).then(async cache=>{
  const asset=paths.get(path),saved=await cache.match(path);
  if(await verified(saved,asset))return saved;
  try{const response=await download(asset);await cache.put(path,response.clone());return response;}catch{return Response.error();}
 }));
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
