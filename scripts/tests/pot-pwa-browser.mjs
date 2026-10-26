// Isolated browser/SW contract. Never uses a real PushManager subscription or any
// product root, account, model or push provider. Supply an existing Playwright.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
const root=resolve(import.meta.dirname,'../..');
const calls=[],devices=new Map(),pending=[],queue=[],pendingSessions=[];let holdSession=false,expireSubscription=false,failNext=false,holdDevice=false;const heldDevices=[];
const event={id:'["node","alpha","reply","new"]',space:'node',control:'node',owner:'alpha',task:'task-one',kind:'reply',name:'Alpha',preview:'Synthetic reply complete'};
const shell=`<!doctype html><link rel="stylesheet" href="/styles.css"><main style="padding:48px;max-width:720px"><h1>System notifications</h1><div id="setting"></div></main><script type="module">
import {createSystemNotifications} from '/adapters/notifications.mjs';
import {notificationSetting} from '/components/notification-setting.mjs';
await navigator.serviceWorker.register('/service-worker.js',{type:'module',scope:'/'});const registration=await navigator.serviceWorker.ready;
const fakeSubscription=()=>({endpoint:'https://web.push.apple.com/synthetic-browser-'+(localStorage.getItem('fakeSerial')||0),options:{applicationServerKey:Uint8Array.from(atob((localStorage.getItem('fakeLegacy')?'A':'B')+'A'.repeat(86)),char=>char.charCodeAt(0)).buffer},keys:{p256dh:'BEE',auth:'synthetic'},toJSON(){return {endpoint:this.endpoint,keys:this.keys}},async unsubscribe(){window.pushUnsubscriptions++;localStorage.removeItem('fakeSubscribed');localStorage.removeItem('fakeLegacy');return true}});
const pushManager={async getSubscription(){window.pushReads++;return localStorage.getItem('fakeSubscribed')?fakeSubscription():null},async subscribe(options){window.pushSubscriptions++;window.pushGesture=navigator.userActivation.isActive;localStorage.setItem('fakeSerial',String(Number(localStorage.getItem('fakeSerial')||0)+1));localStorage.setItem('fakeSubscribed','true');return fakeSubscription()}};
Object.defineProperty(registration,'pushManager',{value:pushManager});
let locale='en';const client={production:true,desktop:false,runtime:{runtime_id:'node'},spaces:[{id:'local',control_id:'node',connected:true}],transport:async(path,options={})=>{const response=await fetch(path,{method:options.method||'GET',signal:options.signal,headers:options.body?{'Content-Type':'application/json'}:{},body:options.body?JSON.stringify(options.body):undefined});if(!response.ok)throw Object.assign(Error('Synthetic API '+response.status),{status:response.status});return response.json()}};
window.opened=[];window.manager=createSystemNotifications({client,surface:'main',view:()=>({space:'local',owner:'other',route:'chat'}),open:target=>window.opened.push(target),locale:()=>locale});
const setting=notificationSetting({notifications:manager,locale:()=>locale,desktop:false});document.querySelector('#setting').append(setting.element);await manager.directory();manager.ready();
window.changeLocale=()=>{locale='zh-CN';dispatchEvent(new Event('pot:locale'))};
window.addEventListener('pagehide',()=>{setting.dispose();manager.dispose()},{once:true});
</script>`;
const server=createServer(async(req,res)=>{
 try{
  const url=new URL(req.url,'http://fixture');
  if(url.pathname.startsWith('/api/')){
   const chunks=[];for await(const chunk of req)chunks.push(chunk);const data=chunks.length?JSON.parse(Buffer.concat(chunks)):null;calls.push({path:url.pathname,data});res.setHeader('Content-Type','application/json');
   if(url.pathname.endsWith('/session')){if(holdSession){pendingSessions.push(res);return}res.end(JSON.stringify({binding:'synthetic-session',control:'node'}));return}
   if(url.pathname.endsWith('/key')){res.end(JSON.stringify({public_key:'B'+ 'A'.repeat(86)}));return}
   if(url.pathname.endsWith('/device')){if(holdDevice&&data.permission==='granted'){devices.set(data.device,data);heldDevices.push(()=>res.end('{"accepted":true}'));return}if(data.channel==='push'&&expireSubscription){expireSubscription=false;res.writeHead(410).end('{"error":"synthetic expired subscription"}');return}devices.set(data.device,data)}
   if(url.pathname.endsWith('/remove'))devices.delete(data.device);
   if(url.pathname.endsWith('/next')){if(failNext){failNext=false;res.writeHead(503).end('{}');return}if(queue.length){res.end(JSON.stringify({delivery:queue.shift()}));return}pending.push(res);res.on('close',()=>{const i=pending.indexOf(res);if(i>=0)pending.splice(i,1)});return}
   res.end('{"accepted":true}');return;
  }
  if(url.pathname==='/'){res.setHeader('Content-Type','text/html');res.end(shell);return}
  const path=url.pathname.startsWith('/pkg/storage/migrations/')?resolve(root,'.'+url.pathname):resolve(root,'web/src','.'+url.pathname);
  if(!path.startsWith(root+'/')){res.writeHead(404).end();return}
  const data=await readFile(path);res.setHeader('Content-Type',({'.js':'text/javascript','.mjs':'text/javascript','.json':'application/json','.css':'text/css','.png':'image/png','.html':'text/html','.ttf':'font/ttf'})[extname(path)]||'application/octet-stream');res.end(data);
 }catch{res.writeHead(404).end()}
});await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const origin=`http://127.0.0.1:${server.address().port}`;
const browser=await chromium.launch({...(process.env.POT_CHROME?{executablePath:process.env.POT_CHROME}:{}),headless:true,args:['--no-sandbox']});
try{
 for(const mode of ['desktop','desktop-pwa','ios-pwa','late-logout']){
  holdSession=mode==='late-logout';calls.length=0;devices.clear();queue.length=0;pending.splice(0).forEach(res=>res.end('{"delivery":null}'));
  const context=await browser.newContext({viewport:{width:1280,height:800},permissions:['notifications']});
  await context.addInitScript(mode=>{
   window.pushReads=0;window.pushSubscriptions=0;window.pushUnsubscriptions=0;
   if(mode==='ios-pwa'&&location.origin!=='null'&&!localStorage.getItem('fakePrepared')){localStorage.setItem('fakePrepared','true');localStorage.setItem('fakeSubscribed','true');localStorage.setItem('fakeLegacy','true')}window.permissionPrompts=0;window.localNotifications=[];
   Object.defineProperty(navigator,'userAgent',{value:mode==='ios-pwa'?'iPhone':'Desktop Chrome'});Object.defineProperty(navigator,'standalone',{value:mode!=='desktop'});
   class FakeNotification{static get permission(){return localStorage.getItem('fakePermission')||'default'}static requestPermission(){window.permissionPrompts++;window.permissionGesture=navigator.userActivation.isActive;localStorage.setItem('fakePermission','granted');return Promise.resolve('granted')}constructor(title,options){window.localNotifications.push({title,options});window.latestNotification=this}close(){}}
   Object.defineProperty(window,'Notification',{configurable:true,writable:true,value:FakeNotification});
  },mode);
  const page=await context.newPage();page.setDefaultTimeout(7000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(origin);await page.getByRole('button',{name:'Allow notifications',exact:true}).waitFor();
  assert.equal(await page.evaluate(()=>permissionPrompts),0);
  if(mode==='late-logout'){
    for(let i=0;i<100&&!pendingSessions.length;i++)await page.waitForTimeout(10);
    assert(pendingSessions.length);await page.evaluate(async()=>{const {disableNotificationInstallation}=await import('/adapters/pwapush.mjs');await disableNotificationInstallation()});
    holdSession=false;pendingSessions.splice(0).forEach(res=>res.end(JSON.stringify({binding:'synthetic-session',control:'node'})));
    await page.waitForFunction(()=>manager.state.error.includes('notification_session_retired'));
    assert(!calls.some(call=>call.path.endsWith('/device')),'late session read reactivated a logged-out installation');
    await context.close();console.log('PASS late logout: an in-flight session read cannot reactivate installation state');continue;
  }
  if(mode!=='ios-pwa'){assert.equal(await page.evaluate(()=>pushReads+pushSubscriptions),0);assert(!calls.some(x=>x.path.endsWith('/key')))}
  if(mode==='ios-pwa'){assert(!Array.from(devices.values()).some(device=>device.channel==='push'),'legacy VAPID subscription was adopted without explicit setup')}
  await page.getByRole('button',{name:'Allow notifications',exact:true}).click();
  await page.waitForFunction(()=>manager.state.permission==='granted');assert.equal(await page.evaluate(()=>permissionPrompts),1);assert.equal(await page.evaluate(()=>permissionGesture),true);
  await page.waitForFunction(()=>!document.querySelector('.notification-setting button:not([hidden])'));
  if(mode==='ios-pwa'){
   await page.waitForFunction(()=>pushSubscriptions===1);assert.equal(await page.evaluate(()=>pushUnsubscriptions),1,'legacy subscription was not replaced in the explicit setup gesture');assert.equal(await page.evaluate(()=>pushReads>0),true);
   for(let i=0;i<100&&!Array.from(devices.values()).some(d=>d.channel==='push');i++)await page.waitForTimeout(10);
   const device=Array.from(devices.values()).find(d=>d.channel==='push');assert(device);
   await page.reload();await page.waitForFunction(()=>manager.state.permission==='granted');await page.waitForTimeout(150);assert.equal(await page.evaluate(()=>pushSubscriptions),0,'reload must reuse subscription');
   const control=await context.newPage();await control.goto('about:blank');const cdp=await context.newCDPSession(control);const registrations=[];cdp.on('ServiceWorker.workerRegistrationUpdated',e=>registrations.push(...e.registrations));await cdp.send('ServiceWorker.enable');
   for(let i=0;i<100&&!registrations.some(r=>r.scopeURL===origin+'/');i++)await page.waitForTimeout(10);
   const registration=registrations.find(r=>r.scopeURL===origin+'/');assert(registration,'real ServiceWorker registration missing');
   await page.close();
   await cdp.send('ServiceWorker.deliverPushMessage',{origin,registrationId:registration.registrationId,data:JSON.stringify({version:1,event,device:device.device,title:'Pot · Alpha',body:'Synthetic reply complete'})});
   const worker=context.serviceWorkers()[0];assert(worker);
   let visible=[];for(let i=0;i<100;i++){visible=await worker.evaluate(async()=>{const rows=await self.registration.getNotifications();return rows.map(row=>({title:row.title,tag:row.tag,data:row.data}))});if(visible.length)break;await control.waitForTimeout(10)}
   assert.equal(visible.length,1,'closed page Push must show a real SW notification');assert.equal(visible[0].data.event.task,'task-one');
   // A queued message for another installation is fenced before display.
   await cdp.send('ServiceWorker.deliverPushMessage',{origin,registrationId:registration.registrationId,data:JSON.stringify({version:1,event:{...event,id:'foreign'},device:'other-device',title:'Wrong',body:'Wrong'})});
   await control.waitForTimeout(100);assert.equal(await worker.evaluate(async()=>(await self.registration.getNotifications()).length),1);
   const clickPage=await context.newPage();await clickPage.goto(origin);await clickPage.waitForFunction(()=>manager.state.permission==='granted');
   // Synthetic click dispatch exercises the real worker handler and real page
   // message/IndexedDB recovery. OS notification-center activation is untested.
   await worker.evaluate(async()=>{
    self.fixtureNotification=(await self.registration.getNotifications())[0];
    const match=self.clients.matchAll.bind(self.clients);const windows=await match({type:'window',includeUncontrolled:true});
    for(const window of windows)Object.defineProperty(window,'focus',{value:async()=>window});
    self.clients.matchAll=async()=>windows;
    const click=new NotificationEvent('notificationclick',{notification:self.fixtureNotification});let done;Object.defineProperty(click,'waitUntil',{value:promise=>done=promise});self.dispatchEvent(click);await done;
    self.clients.matchAll=match;
   });
   await clickPage.waitForFunction(()=>opened[0]?.task==='task-one');assert.equal(await clickPage.evaluate(()=>opened[0].space),'local');await clickPage.close();
   const destination=await worker.evaluate(async()=>{
    const match=self.clients.matchAll,open=self.clients.openWindow;let destination;
    self.clients.matchAll=async()=>[];self.clients.openWindow=async url=>{destination=url};
    const click=new NotificationEvent('notificationclick',{notification:self.fixtureNotification});let done;Object.defineProperty(click,'waitUntil',{value:promise=>done=promise});self.dispatchEvent(click);await done;
    self.clients.matchAll=match;self.clients.openWindow=open;return destination;
   });assert.equal(destination,'/');
   const restored=await context.newPage();await restored.goto(origin);await restored.waitForFunction(()=>opened[0]?.task==='task-one');
   await restored.evaluate(()=>manager.setEnabled(false));assert(!devices.has(device.device));
   await cdp.send('ServiceWorker.deliverPushMessage',{origin,registrationId:registration.registrationId,data:JSON.stringify({version:1,event:{...event,id:'after-disable'},device:device.device,title:'Disabled',body:'Disabled'})});await restored.waitForTimeout(100);
   assert.equal(await worker.evaluate(async()=>(await self.registration.getNotifications()).length),0);
   expireSubscription=true;await restored.evaluate(()=>manager.setEnabled(true));await restored.waitForFunction(()=>manager.state.needsSubscription===true);
   assert.equal(await restored.evaluate(()=>pushUnsubscriptions),1,'invalid subscription was not cleared');assert.equal(await restored.evaluate(()=>pushSubscriptions),0,'invalid subscription was silently replaced');
   await restored.getByRole('button',{name:'Allow notifications',exact:true}).click();await restored.waitForFunction(()=>!manager.state.needsSubscription);
   assert.equal(await restored.evaluate(()=>permissionPrompts),0,'granted permission was requested again');assert.equal(await restored.evaluate(()=>pushSubscriptions),1);
   const replacement=devices.get(device.device);assert(replacement);assert.notEqual(replacement.subscription.endpoint,device.subscription.endpoint);
   await restored.close();await control.close();
  }else{
   const delivery={event,attempt:1};if(pending.length)pending.shift().end(JSON.stringify({delivery}));else queue.push(delivery);
   await page.waitForFunction(()=>localNotifications.length===1).catch(async error=>{console.error({state:await page.evaluate(()=>manager.state),calls,pending:pending.length,queue:queue.length,errors});throw error});assert.equal(await page.evaluate(()=>pushReads+pushSubscriptions),0,'local-capable PWA registered Push');
   assert(!calls.some(x=>x.data?.channel==='push'||x.path.endsWith('/key')));await page.evaluate(()=>latestNotification.onclick());assert.equal(await page.evaluate(()=>opened[0].task),'task-one');assert.equal(await page.evaluate(()=>opened[0].space),'local');
   const registrations=calls.filter(call=>call.path.endsWith('/device')).length,views=calls.filter(call=>call.path.endsWith('/view')).length;
   await page.evaluate(async()=>{for(let i=0;i<20;i++){await manager.directory();await manager.report()}});
   assert.equal(calls.filter(call=>call.path.endsWith('/device')).length,registrations,'directory updates rewrote registration');assert(calls.filter(call=>call.path.endsWith('/view')).length<=views+1,'unchanged view created lease traffic');
   await page.evaluate(()=>{window.Notification=class extends Notification{constructor(){throw Error('opaque notification result')}}});
   const uncertain={event:{...event,id:event.id+'-uncertain'},attempt:1};if(pending.length)pending.shift().end(JSON.stringify({delivery:uncertain}));else queue.push(uncertain);
   for(let i=0;i<100&&!calls.some(call=>call.data?.event===uncertain.event.id&&call.data?.state==='unknown');i++)await page.waitForTimeout(10);
   assert(calls.some(call=>call.data?.event===uncertain.event.id&&call.data?.state==='unknown'),'opaque local result was treated as safely retryable');
   assert(!calls.some(call=>call.data?.channel==='push'));
   const sibling=await context.newPage();await sibling.goto(origin);await sibling.waitForFunction(()=>manager.state.permission==='granted');await sibling.waitForTimeout(100);
   assert.equal(pending.length,1,'multiple tabs must share one notification HTTP receiver');
   await page.evaluate(()=>document.dispatchEvent(new Event('freeze')));await sibling.waitForTimeout(100);assert.equal(pending.length,1,'sibling must take over the released receiver');
   const beforeResume=Math.max(...calls.filter(call=>call.path.endsWith('/view')).map(call=>call.data.view.sequence));
   await page.evaluate(()=>document.dispatchEvent(new Event('resume')));await page.waitForTimeout(100);assert.equal(pending.length,1);assert(Math.max(...calls.filter(call=>call.path.endsWith('/view')).map(call=>call.data.view.sequence))>beforeResume,'resume reset the reading sequence');
   await sibling.close();await page.waitForTimeout(100);assert.equal(pending.length,1);
   await page.evaluate(()=>dispatchEvent(new Event('offline')));await page.waitForTimeout(100);assert.equal(pending.length,0,'offline page retained its notification request');
   await page.evaluate(()=>dispatchEvent(new Event('online')));for(let i=0;i<100&&pending.length!==1;i++)await page.waitForTimeout(10);assert.equal(pending.length,1,'notification receiver did not resume');
   await page.evaluate(()=>changeLocale());await page.getByRole('checkbox',{name:'系统通知'}).waitFor();
   await page.getByRole('checkbox',{name:'系统通知'}).uncheck();assert.equal(await page.evaluate(()=>manager.state.enabled),false);
  }
  assert.deepEqual(errors,[]);await context.close();console.log(`PASS ${mode}: channel, gesture, subscription lifecycle, delivery and device fencing`);
 }

 // Real fetch/IndexedDB/SW lifecycle interleavings, with no real subscriptions.
 for(const scenario of ['recover','dispose','freeze','off','reset','denied','cancel-retry','deny-retry','push-freeze','push-dispose','push-off']){
  calls.length=0;devices.clear();queue.length=0;failNext=scenario==='recover'||scenario==='cancel-retry'||scenario==='deny-retry';
  const context=await browser.newContext({viewport:{width:1280,height:800}});
  await context.addInitScript(scenario=>{
   window.pushReads=window.pushSubscriptions=window.pushUnsubscriptions=0;window.localNotifications=[];
   const intervals=new Set(),set=window.setInterval.bind(window),clear=window.clearInterval.bind(window);window.intervals=intervals;
   window.setInterval=(...args)=>{const id=set(...args);intervals.add(id);return id};window.clearInterval=id=>{intervals.delete(id);clear(id)};
   Object.defineProperty(navigator,'userAgent',{value:scenario.startsWith('push-')?'iPhone':'Desktop Chrome'});Object.defineProperty(navigator,'standalone',{value:true});
   class FakeNotification{static get permission(){return localStorage.getItem('fakePermission')||'default'}static requestPermission(){localStorage.setItem('fakePermission','granted');return Promise.resolve('granted')}constructor(title,options){localNotifications.push({title,options})}close(){}}
   Object.defineProperty(window,'Notification',{value:FakeNotification});
  },scenario);
  const page=await context.newPage();page.setDefaultTimeout(7000);await page.goto(origin);
  await page.getByRole('button',{name:'Allow notifications',exact:true}).waitFor();
  // Let the initial default-permission sync settle before holding the grant.
  const initial=scenario.startsWith('push-')?'/remove':'/device';
  for(let i=0;i<200&&!calls.some(call=>call.path.endsWith(initial));i++)await page.waitForTimeout(10);
  assert(calls.some(call=>call.path.endsWith(initial)));
  await page.waitForTimeout(30);
  holdDevice=!['recover','cancel-retry','deny-retry'].includes(scenario);
  await page.getByRole('button',{name:'Allow notifications',exact:true}).click();
  if(['recover','cancel-retry','deny-retry'].includes(scenario)){
   await page.waitForFunction(()=>manager.state.error.includes('503'));
   if(scenario!=='recover'){
    await page.evaluate(async scenario=>{if(scenario==='cancel-retry')await manager.setEnabled(false);else{localStorage.setItem('fakePermission','denied');dispatchEvent(new Event('focus'))}},scenario);await page.waitForTimeout(700);
    assert.equal(calls.filter(call=>call.path.endsWith('/next')).length,1,'disabled receiver retried');
    assert.equal(await page.evaluate(()=>intervals.size),0);
   }else{
    // Current queued work must survive transport retry; no second baseline.
    const sibling=await context.newPage();await sibling.goto(origin);await sibling.waitForTimeout(100);
    assert.equal(calls.filter(call=>call.path.endsWith('/baseline')).length,1,'standby tab rebuilt the live receiver baseline during backoff');
    queue.push({event:{...event,id:'current-after-503'},attempt:1});
    await page.waitForFunction(()=>localNotifications.length===1);
    assert.equal(calls.filter(call=>call.path.endsWith('/baseline')).length,1);
    assert.equal(await page.evaluate(()=>intervals.size),1);
    assert.equal(await page.evaluate(()=>manager.state.error),'');
    for(let i=0;i<100&&pending.length!==1;i++)await page.waitForTimeout(10);
    assert.equal(pending.length,1,'transport retry lost the installation receiver lock');
    queue.push({event:{...event,id:'current-during-handoff'},attempt:1});await page.evaluate(()=>manager.dispose());
    await sibling.waitForFunction(()=>localNotifications.length===1);
    assert.equal(calls.filter(call=>call.path.endsWith('/baseline')).length,1,'standby takeover expired current queued work');await sibling.close();
   }
  }else{
   for(let i=0;i<200&&!heldDevices.length;i++)await page.waitForTimeout(10);assert(heldDevices.length);
   const before=calls.filter(call=>['/baseline','/view','/next'].some(suffix=>call.path.endsWith(suffix))).length;
   await page.evaluate(scenario=>{
    if(scenario==='dispose'||scenario==='push-dispose')manager.dispose();
    if(scenario==='freeze'||scenario==='push-freeze')document.dispatchEvent(new Event('freeze'));
    if(scenario==='off'||scenario==='push-off'){window.off=manager.setEnabled(false);}
    if(scenario==='reset')manager.refresh({reset:true});
    if(scenario==='denied'){localStorage.setItem('fakePermission','denied');dispatchEvent(new Event('focus'));}
   },scenario);
   if(scenario==='off'||scenario==='push-off')await page.waitForFunction(async()=>{
    const {notificationInstallation}=await import('/adapters/notification-installation.mjs');return (await notificationInstallation(store=>store.get('device')))?.enabled===false;
   });
   holdDevice=false;heldDevices.splice(0).forEach(done=>done());await page.waitForTimeout(250);
   if(scenario==='off'||scenario==='push-off'){await page.evaluate(()=>window.off);assert(!Array.from(devices.values()).some(device=>device.enabled),'disabled installation retained server registration')}
   assert.equal(calls.filter(call=>['/baseline','/view','/next'].some(suffix=>call.path.endsWith(suffix))).length,before,'stale registration started notification IO: '+scenario);
   assert.equal(await page.evaluate(()=>intervals.size),0,'stale registration started a lease timer: '+scenario);
   if(scenario==='off'||scenario==='push-off')assert.equal(await page.evaluate(async()=>{const {notificationInstallation}=await import('/adapters/notification-installation.mjs');return (await notificationInstallation(store=>store.get('device'))).enabled}),false);
   if(scenario.startsWith('push-')&&scenario!=='push-off'){assert(Array.from(devices.values()).some(device=>device.channel==='push'&&device.enabled),'page retirement removed background Push');assert.equal(await page.evaluate(async()=>{const {notificationInstallation}=await import('/adapters/notification-installation.mjs');return (await notificationInstallation(store=>store.get('device'))).enabled}),true)}
   if(scenario==='freeze'||scenario==='push-freeze'){
    await page.evaluate(()=>document.dispatchEvent(new Event('resume')));await page.waitForFunction(()=>intervals.size===1);
   }
  }
  if(!scenario.startsWith('push-'))assert.equal(await page.evaluate(()=>pushReads+pushSubscriptions),0,'local recovery touched Push');
  await context.close();pending.splice(0).forEach(res=>res.end('{"delivery":null}'));console.log('PASS receiver lifecycle: '+scenario);
 }
}finally{await browser.close();for(const res of pending)res.destroy();await new Promise(resolve=>server.close(resolve))}
