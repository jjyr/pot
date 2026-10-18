import assert from 'node:assert/strict';
import {createNotificationProjection,viewingNotification,notificationText} from '../../web/src/state/notifications.mjs';
import {createSystemNotifications,browserNotificationPermission} from '../../web/src/adapters/notifications.mjs';

const owner={id:'alpha',space_id:'space-a',control_id:'control-a',name:'Alpha',reply_id:'old',reply_count:4};
const task=(phase,id='task')=>({id,title:'Build',phase});
const approval=(id='approval',task='task')=>({id,request:{owner:'alpha',task,args:{secret:'never put this in notification'}}});
const projection=createNotificationProjection();
assert.deepEqual(projection.observe(owner,[task('complete')],[approval()],'boot'),[],'history is a baseline');
assert.deepEqual(projection.observe(owner,[task('complete')],[approval()],'boot'),[],'duplicate snapshot');
assert.deepEqual(projection.observe({...owner,responding:true},[task('running')],[],'boot'),[],'tokens/running states do not notify');
const events=projection.observe({...owner,reply_id:'new',reply_count:5},[task('failed')],[approval('new')],'boot');
assert.deepEqual(events.map(event=>event.kind),['reply','task-failed','approval']);
assert.equal(events[2].name,'Build','Task approval uses its Task name');
assert(!JSON.stringify(events.map(event=>notificationText(event))).includes('secret'));
assert.equal(projection.observe({...owner,reply_id:'new',reply_count:5},[task('interrupted')],[],'boot')[0].kind,'task-failed');
projection.reset('space-a');
assert.deepEqual(projection.observe({...owner,reply_id:'catch-up',reply_count:10},[task('complete')],[approval()],'boot'),[],'reconnect catch-up suppressed');
assert.deepEqual(projection.observe({...owner,reply_id:'restart',reply_count:11},[task('failed')],[approval('restart')],'new-boot'),[],'boot replacement suppressed');
const splitBoot=createNotificationProjection();splitBoot.observe(owner,[task('running')],[],'boot-one');
splitBoot.observe({...owner,reply_id:'restart-reply',reply_count:20},undefined,undefined,'boot-two');
assert.deepEqual(splitBoot.observe({...owner,reply_id:'restart-reply',reply_count:20},[task('complete')],[approval('restart')],'boot-two'),[],'new-boot directory and asynchronous Task/approval reads share a fresh baseline');
const event={space:'space-a',control:'control-a',owner:'alpha',task:'task'};
assert(viewingNotification({focused:true,route:'chat',space:'space-a',owner:'alpha'},event));
assert(viewingNotification({focused:true,route:'tasks',space:'space-a',owner:'alpha',task:'task'},event));
for(const view of [{focused:false,route:'chat',space:'space-a',owner:'alpha'},{focused:true,route:'settings',space:'space-a',owner:'alpha'},
 {focused:true,route:'chat',space:'space-b',owner:'alpha'},{focused:true,route:'chat',space:'space-a',owner:'beta'},
 {focused:true,route:'tasks',space:'space-a',owner:'alpha',task:'other'}])assert(!viewingNotification(view,event));
for(const permission of ['default','granted','denied'])assert.equal(browserNotificationPermission({isSecureContext:true,Notification:{permission}}),permission);
assert.equal(browserNotificationPermission({isSecureContext:true}),'unsupported');
assert.equal(browserNotificationPermission({isSecureContext:false,Notification:{permission:'granted'}}),'insecure');

// Exercise the running notification coordinator, with OS delivery replaced by
// a recorder. No test requests real desktop permission or posts desktop alerts.
const storage=new Map(),seen=new Set(),shown=[],opened=[];
let permission='default',requests=0,focused=true,route={route:'chat',space:'space-a',owner:'alpha'},tasks=[],approvals=[];
class TestNotification {
 static get permission(){return permission;}
 static async requestPermission(){requests++;return permission='granted';}
 constructor(title,options){this.title=title;this.options=options;shown.push(this);}
 close(){this.closed=true;}
}
function host(){const host=new EventTarget(),document=new EventTarget();document.visibilityState='visible';document.hasFocus=()=>focused;
 return Object.assign(host,{document,Notification:TestNotification,isSecureContext:true,crypto:globalThis.crypto,BroadcastChannel,
 localStorage:{getItem:key=>storage.get(key)??null,setItem:(key,value)=>storage.set(key,value)},setTimeout,focus(){focused=true;}});}
const client=()=>({desktop:false,assistants:[{...owner}],spaces:[{id:'space-a',boot_id:'boot',connected:true}],
 request:async(path,scope)=>{assert.equal(scope.owner,'alpha');assert.equal(scope.space,'space-a');return path.startsWith('/api/v1/tasks/query')?{items:tasks,next_offset:null}:{pending:approvals};}});
const claims=async id=>{if(seen.has(id))return false;seen.add(id);return true;};
const clientA=client(),hostA=host();
const manager=createSystemNotifications({client:clientA,surface:'main',view:()=>route,open:event=>opened.push(event),locale:()=> 'en',host:hostA,claim:claims});
assert.equal(manager.state.enabled,true);assert.equal(manager.state.permission,'default');assert.equal(requests,0,'mount never prompts');
await manager.directory();assert.equal(shown.length,0);
await manager.authorize();assert.equal(requests,1);assert.equal(manager.state.permission,'granted');
clientA.assistants[0]={...owner,reply_id:'reply-visible',reply_count:5};await manager.directory();assert.equal(shown.length,0,'focused corresponding chat suppressed');
focused=false;clientA.assistants[0]={...owner,reply_id:'reply-background',reply_count:6};await manager.directory();assert.equal(shown.length,1,'background window delivers');
shown[0].onclick();assert.equal(opened[0].space,'space-a');assert.equal(opened[0].owner,'alpha');assert(shown[0].closed);
focused=true;route={route:'chat',space:'space-a',owner:'beta'};
clientA.assistants[0]={...owner,reply_id:'reply-other-chat',reply_count:7};await manager.directory();assert.equal(shown.length,2,'foreground other chat delivers');
tasks=[task('running')];await manager.refresh();tasks=[task('complete')];await manager.refresh();assert.equal(shown.at(-1).options.body,'Background task complete');
approvals=[approval()];await manager.refresh();assert.equal(shown.at(-1).options.body,'Approval required. Review in Pot.');
const beforeReplay=shown.length;await manager.refresh();await manager.directory();assert.equal(shown.length,beforeReplay);
await manager.setEnabled(false);assert.equal(manager.state.enabled,false);
clientA.assistants[0]={...owner,reply_id:'while-disabled',reply_count:8};await manager.directory();assert.equal(shown.length,beforeReplay,'off suppresses a new event');
const clientB=client(),hostB=host(),managerB=createSystemNotifications({client:clientB,surface:'main',view:()=>route,open:event=>opened.push(event),locale:()=> 'en',host:hostB,claim:claims});
assert.equal(managerB.state.enabled,false,'disabled preference persists for the browser client');
await managerB.setEnabled(true);hostA.dispatchEvent(Object.assign(new Event('storage'),{key:'pot-system-notifications-browser-v1'}));
await managerB.directory();
// Both tabs observe the same new event; only one atomic claim can deliver it.
clientA.assistants[0]=clientB.assistants[0]={...owner,reply_id:'multi-tab',reply_count:10};
const beforeTabs=shown.length;await Promise.all([manager.directory(),managerB.directory()]);assert.equal(shown.length,beforeTabs+1);
// A tab viewing the target suppresses the other tab as well.
route={route:'chat',space:'space-a',owner:'alpha'};
clientA.assistants[0]=clientB.assistants[0]={...owner,reply_id:'multi-tab-viewed',reply_count:11};
await Promise.all([manager.directory(),managerB.directory()]);assert.equal(shown.length,beforeTabs+1);
await manager.refresh({reset:true,space:'space-a'});clientA.assistants[0]={...owner,reply_id:'reconnect',reply_count:100};await manager.directory();assert.equal(shown.length,beforeTabs+1);
// An old in-flight catalogue result cannot become the new connection baseline.
const staleEpoch=manager.epoch;await manager.refresh({reset:true,space:'space-a'});
clientA.assistants[0]={...owner,reply_id:'stale-read',reply_count:101};await manager.directory({epoch:staleEpoch});
clientA.assistants[0]={...owner,reply_id:'new-connection-history',reply_count:102};await manager.directory();assert.equal(shown.length,beforeTabs+1);
// Page visibility and focus are independent inputs.
route={route:'chat',space:'space-a',owner:'alpha'};focused=true;hostA.document.visibilityState='hidden';hostB.document.visibilityState='hidden';
clientA.assistants[0]={...owner,reply_id:'hidden-page',reply_count:103};await manager.directory();assert.equal(shown.length,beforeTabs+2);
hostA.document.visibilityState='visible';hostB.document.visibilityState='visible';
permission='denied';hostA.dispatchEvent(new Event('focus'));assert.equal(manager.state.permission,'denied');await manager.authorize();assert.equal(requests,1,'denied permission does not prompt repeatedly');
manager.dispose();managerB.dispose();

// Native click recovery waits for the directory. Its preference never writes
// the Web client's local switch, even when both observe the same Control.
storage.set('pot-system-notifications-browser-v1','false');
let nativeClick,nativeEnabled=true,nativePrompts=0;const nativeOpened=[];
const nativeClient={...client(),desktop:true,assistants:[],notificationStatus:async()=>({enabled:nativeEnabled,permission:'default'}),
 setNotificationsEnabled:async value=>{nativeEnabled=value;},requestNotificationPermission:async()=>{nativePrompts++;},
 notifySystem:async()=>{},notificationView:async()=>{},onNotificationClick:handler=>{nativeClick=handler;return ()=>{};}};
const native=createSystemNotifications({client:nativeClient,surface:'main',view:()=>route,open:event=>nativeOpened.push(event),locale:()=> 'en',host:host()});
nativeClick({...event,kind:'approval'});assert.equal(nativeOpened.length,0);
nativeClient.assistants=[{...owner}];await native.directory();assert.equal(nativeOpened[0].task,'task');assert.equal(nativePrompts,0);
await native.setEnabled(false);await native.setEnabled(true);assert.equal(storage.get('pot-system-notifications-browser-v1'),'false');native.dispose();

// A resumed Task may finish again; its durable update timestamp identifies the
// new terminal transition while repeated metadata still yields no event.
const resumed=createNotificationProjection();resumed.observe(owner,[{...task('running'),updated_at_ms:1}],[]);
const firstFinish=resumed.observe(owner,[{...task('complete'),updated_at_ms:2}],[])[0];
resumed.observe(owner,[{...task('running'),updated_at_ms:3}],[]);
const nextFinish=resumed.observe(owner,[{...task('complete'),updated_at_ms:4}],[])[0];assert.notEqual(firstFinish.id,nextFinish.id);

// Retirement is local and best-effort. Removed server endpoints (404), browser
// restrictions and a stalled push provider cannot block the new coordinator.
const {retireBrowserPush}=await import('../../web/src/adapters/access.mjs');
const originalNavigator=Object.getOwnPropertyDescriptor(globalThis,'navigator'),originalFetch=globalThis.fetch;
let retired=0,serverCalls=0;
const browser=value=>Object.defineProperty(globalThis,'navigator',{configurable:true,value});
try{
 globalThis.fetch=async()=>{serverCalls++;return {ok:false,status:404};};
 browser({serviceWorker:{getRegistration:async()=>({pushManager:{getSubscription:async()=>({unsubscribe:async()=>{retired++;}})}})}});
 await retireBrowserPush();assert.equal(retired,1);assert.equal(serverCalls,0);
 browser({});await retireBrowserPush();
 browser({serviceWorker:{getRegistration:async()=>undefined}});await retireBrowserPush();
 for(const operation of ['getRegistration','getSubscription','unsubscribe']){
  const fail=async()=>{throw Error('Browser denied cleanup');};
  browser({serviceWorker:{getRegistration:operation==='getRegistration'?fail:async()=>({pushManager:{getSubscription:operation==='getSubscription'?fail:async()=>({unsubscribe:fail})}})}});
  await retireBrowserPush();
 }
 let finishCleanup;
 browser({serviceWorker:{getRegistration:async()=>({pushManager:{getSubscription:async()=>({unsubscribe:()=>new Promise(resolve=>{finishCleanup=resolve;})})}})}});
 const cleanup=retireBrowserPush();await new Promise(resolve=>setImmediate(resolve));
 permission='granted';storage.delete('pot-system-notifications-browser-v1');focused=false;route={route:'chat',space:'space-a',owner:'beta'};
 const upgradedClient=client(),before=shown.length,prompts=requests;
 const upgraded=createSystemNotifications({client:upgradedClient,surface:'main',view:()=>route,open:()=>{},locale:()=> 'en',host:host(),claim:claims});
 await upgraded.directory();assert.equal(upgraded.state.enabled,true);assert.equal(upgraded.state.permission,'granted');
 upgradedClient.assistants[0]={...owner,reply_id:'after-retirement',reply_count:5};await upgraded.directory();
 assert.equal(shown.length,before+1);assert.equal(requests,prompts);assert.equal(serverCalls,0);
 upgraded.dispose();finishCleanup(true);await cleanup;
}finally{
 if(originalNavigator)Object.defineProperty(globalThis,'navigator',originalNavigator);else delete globalThis.navigator;
 globalThis.fetch=originalFetch;
}

// The replacement PWA worker takes over without an open-tab shutdown and
// removes only Pot's obsolete asset caches. It cannot deliver offline alerts.
const {readFile}=await import('node:fs/promises'),{runInNewContext}=await import('node:vm');
const workerSource=await readFile(new URL('../../web/src/service-worker.js',import.meta.url),'utf8');
const workerHandlers=new Map(),deleted=[],cached=[];let skipped=0,claimed=0;
runInNewContext(workerSource,{
 self:{addEventListener:(name,handler)=>workerHandlers.set(name,handler),skipWaiting:async()=>{skipped++;},clients:{claim:async()=>{claimed++;}}},
 caches:{open:async()=>({addAll:async assets=>{cached.push(...assets);}}),keys:async()=>['pot-go-ui-v1','pot-go-ui-v2','other-app'],delete:async key=>{deleted.push(key);}},
});
for(const name of ['install','activate']){let effect;workerHandlers.get(name)({waitUntil:promise=>{effect=promise;}});await effect;}
assert.equal(skipped,1);assert.equal(claimed,1);assert.deepEqual(deleted,['pot-go-ui-v1']);assert(cached.includes('/app.js'));
assert.equal(workerHandlers.has('push'),false);assert.equal(workerHandlers.has('notificationclick'),false);assert(workerHandlers.has('fetch'));

// No browser permission state can implicitly grant permission during delivery.
focused=false;route={route:'chat',space:'space-a',owner:'beta'};
storage.set('pot-system-notifications-browser-v1','true');
for(const state of ['default','denied','unsupported','insecure']){
 permission=state;const testHost=host(),testClient=client(),before=shown.length,prompts=requests;
 if(state==='unsupported')delete testHost.Notification;
 if(state==='insecure')testHost.isSecureContext=false;
 const coordinator=createSystemNotifications({client:testClient,surface:'main',view:()=>route,open:()=>{},locale:()=> 'en',host:testHost,claim:claims});
 await coordinator.directory();testClient.assistants[0]={...owner,reply_id:`permission-${state}`,reply_count:5};await coordinator.directory();
 assert.equal(shown.length,before,`${state} does not deliver`);assert.equal(requests,prompts,`${state} does not prompt`);coordinator.dispose();
}
console.log('System notification state, coordinator, preference, focus, routing and multi-tab checks passed');
