// No offline transcript cache. Push payloads are decrypted by the browser.
self.addEventListener('install', event => event.waitUntil(self.skipWaiting()));
self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));
// Network only: never cache credentials, transcripts or authenticated responses.
self.addEventListener('fetch', event => {
  if(event.request.method === 'GET' && new URL(event.request.url).origin === self.location.origin)
    event.respondWith(fetch(event.request));
});
self.addEventListener('push',event=>{
  if(!event.data)return;
  let data;try{data=event.data.json();}catch{return;}
  if(data.conversation!=='main'||typeof data.body!=='string'||typeof data.message_id!=='string')return;
  event.waitUntil((async()=>{
    const windows=await self.clients.matchAll({type:'window',includeUncontrolled:true});
    if(windows.some(c=>c.visibilityState==='visible'&&new URL(c.url).origin===self.location.origin&&!new URL(c.url).searchParams.has('task')&&['chat',null].includes(new URL(c.url).searchParams.get('page'))))return;
    await self.registration.showNotification('Pot',{body:data.body,tag:data.tag,renotify:false,icon:'/icon.png?v=pot-klee-2',data:{message_id:data.message_id}});
  })());
});
self.addEventListener('notificationclick',event=>{
  event.notification.close();
  const url='/?message='+encodeURIComponent(event.notification.data.message_id);
  event.waitUntil((async()=>{
    const windows=await self.clients.matchAll({type:'window'});
    if(windows.length){await windows[0].navigate(url);await windows[0].focus();}else{await self.clients.openWindow(url);}
  })());
});
