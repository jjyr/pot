// Only immutable UI assets are cached. API, token and remote desktop traffic
// always goes to the network and never enters the offline cache.
const CACHE='pot-ui-v3';
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
