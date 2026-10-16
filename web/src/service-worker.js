// Only immutable UI assets are cached. API, token and remote desktop traffic
// always goes to the network and never enters the offline cache.
const CACHE='pot-go-ui-v1';
const ASSETS=['/index.html','/app.js','/styles.css','/assets/PotCompanion.png','/assets/PotMask.png','/assets/IBMPlexSans-Regular.ttf'];
self.addEventListener('install',event=>event.waitUntil(caches.open(CACHE).then(cache=>cache.addAll(ASSETS))));
self.addEventListener('activate',event=>event.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(key=>key.startsWith('pot-go-ui-')&&key!==CACHE).map(key=>caches.delete(key))))));
self.addEventListener('fetch',event=>{
 const request=event.request,url=new URL(request.url);
 if(request.method!=='GET'||url.origin!==self.location.origin||url.pathname.startsWith('/api/')||url.pathname.startsWith('/access/')||url.pathname.startsWith('/wails/'))return;
 if(request.mode==='navigate'){event.respondWith(fetch(request).catch(()=>caches.match('/index.html')));return;}
 if(!/\.(?:m?js|css|png|ttf|webmanifest)$/.test(url.pathname))return;
 event.respondWith(fetch(request).then(response=>{if(response.ok){const clone=response.clone();caches.open(CACHE).then(cache=>cache.put(request,clone));}return response;}).catch(()=>caches.match(request)));
});
self.addEventListener('push',event=>{
 let data={};try{data=event.data?.json()||{};}catch{return;}
 event.waitUntil(self.registration.showNotification(data.title||'Pot',{body:data.body||'',icon:'/assets/PotCompanion.png',tag:data.tag||'',data:{assistant_id:data.assistant_id,space_id:data.space_id}}));
});
self.addEventListener('notificationclick',event=>{
 event.notification.close();const data=event.notification.data||{},url=new URL('/',self.location.origin);if(data.assistant_id)url.searchParams.set('assistant_id',data.assistant_id);if(data.space_id)url.searchParams.set('space_id',data.space_id);
 event.waitUntil(clients.openWindow(url.href));
});
