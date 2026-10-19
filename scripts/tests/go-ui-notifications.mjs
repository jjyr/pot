import assert from 'node:assert/strict';
import {createNotificationProjection,taskOutcomeNotifications,viewingNotification,notificationText} from '../../web/src/state/notifications.mjs';
import {createSystemNotifications,browserNotificationPermission} from '../../web/src/adapters/notifications.mjs';

const owner={id:'alpha',space_id:'space-a',control_id:'control-a',name:'Alpha',reply_id:'old',reply_count:4};
const task=(phase,id='task')=>({id,title:'Build',phase});
const approval=(id='approval',task='task')=>({id,request:{owner:'alpha',task,args:{secret:'never put this in notification'}}});
const projection=createNotificationProjection();
assert.deepEqual(projection.observe(owner,[approval()],'boot'),[],'history is a baseline');
assert.deepEqual(projection.observe(owner,[approval()],'boot'),[],'duplicate snapshot');
assert.deepEqual(projection.observe({...owner,responding:true},[],'boot'),[],'token states do not notify');
const events=projection.observe({...owner,reply_id:'new',reply_count:5},[{...approval('new'),task_title:'Build'}],'boot');
assert.deepEqual(events.map(event=>event.kind),['reply','approval']);
assert.equal(events[1].name,'Build','Task approval uses its Task name');
assert(!JSON.stringify(events.map(event=>notificationText(event))).includes('secret'));
projection.reset('space-a');
assert.deepEqual(projection.observe({...owner,reply_id:'catch-up',reply_count:10},[approval()],'boot'),[],'reconnect catch-up suppressed');
assert.deepEqual(projection.observe({...owner,reply_id:'restart',reply_count:11},[approval('restart')],'new-boot'),[],'boot replacement suppressed');
const splitBoot=createNotificationProjection();splitBoot.observe(owner,[],'boot-one');
splitBoot.observe({...owner,reply_id:'restart-reply',reply_count:20},undefined,'boot-two');
assert.deepEqual(splitBoot.observe({...owner,reply_id:'restart-reply',reply_count:20},[approval('restart')],'boot-two'),[],'new-boot directory and asynchronous approval reads share a fresh baseline');
const event={space:'space-a',control:'control-a',owner:'alpha',task:'task'};
assert(viewingNotification({focused:true,route:'chat',space:'space-a',owner:'alpha'},event));
assert(viewingNotification({focused:true,route:'tasks',space:'space-a',owner:'alpha',task:'task'},event));
for(const view of [{focused:false,route:'chat',space:'space-a',owner:'alpha'},{focused:true,route:'settings',space:'space-a',owner:'alpha'},
 {focused:true,route:'chat',space:'space-b',owner:'alpha'},{focused:true,route:'chat',space:'space-a',owner:'beta'},
 {focused:true,route:'tasks',space:'space-a',owner:'alpha',task:'other'}])assert(!viewingNotification(view,event));
for(const permission of ['default','granted','denied'])assert.equal(browserNotificationPermission({isSecureContext:true,Notification:Object.assign(function(){},{permission,requestPermission(){}})}),permission);
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
 request:async(path,scope)=>{assert.equal(scope.owner,'alpha');assert.equal(scope.space,'space-a');if(path.startsWith('/api/v1/tasks/outcomes')){const after=new URL(path,'http://fixture').searchParams.get('after');return {items:after===null?[]:tasks.filter(item=>item.sequence>Number(after)),cursor:tasks.at(-1)?.sequence||0,has_more:false};}assert.equal(path,'/api/v1/permissions');return {pending:approvals};}});
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
tasks=[{...task('complete'),sequence:1}];await manager.refresh();assert.equal(shown.at(-1).options.body,'Background task complete');
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

// Distinct committed terminal transitions keep distinct event IDs even when
// their timestamps coincide. Explicit cancellations remain silent.
const firstFinish=taskOutcomeNotifications(owner,[{...task('complete'),sequence:1,updated_at_ms:2}])[0];
const nextFinish=taskOutcomeNotifications(owner,[{...task('interrupted'),sequence:2,updated_at_ms:2}])[0];
assert.notEqual(firstFinish.id,nextFinish.id);
assert.equal(taskOutcomeNotifications(owner,[task('cancelled')]).length,0);
assert.equal(taskOutcomeNotifications(owner,[task('failed')]).length,0,'Failure must allow assistant recovery before notifying');

// A task query failure or stall cannot clear another owner's baseline, delay
// approvals, or consume the failing owner's last successful outcome cursor.
function isolatedClient(){
 const alerts=[],calls=[],heads=new Map([['alpha',1000],['beta',2000]]),changes=new Map(),pending=new Map();
 let rejectAlpha=false,stallAlpha=null;
 class Alert {static permission='granted';static requestPermission(){};constructor(title,options){alerts.push({title,...options});}}
 const testHost=new EventTarget(),document=new EventTarget();document.visibilityState='hidden';document.hasFocus=()=>false;
 Object.assign(testHost,{document,Notification:Alert,isSecureContext:true,localStorage:{getItem:()=>null},setTimeout});
 const testClient={desktop:false,spaces:[{id:'space-a',boot_id:'boot',connected:true}],assistants:[{...owner},{...owner,id:'beta',name:'Beta'}],
  async request(path,scope){
   calls.push({path,owner:scope.owner});
   if(path==='/api/v1/permissions')return {pending:pending.get(scope.owner)||[]};
   assert(path.startsWith('/api/v1/tasks/outcomes'),'Notification reads must never load Task lists');
   if(scope.owner==='alpha'&&stallAlpha)await stallAlpha;
   if(scope.owner==='alpha'&&rejectAlpha)throw Error('Alpha task read failed');
   const after=new URL(path,'http://fixture').searchParams.get('after');
   return {items:after===null?[]:(changes.get(scope.owner)||[]).filter(item=>item.sequence>Number(after)),cursor:heads.get(scope.owner),has_more:false};
  }};
 const coordinator=createSystemNotifications({client:testClient,surface:'main',view:()=>({}),open:()=>{},locale:()=> 'en',host:testHost,claim:async()=>true});
 return {alerts,calls,heads,changes,pending,client:testClient,coordinator,set reject(value){rejectAlpha=value;},set stall(value){stallAlpha=value;}};
}
const isolated=isolatedClient();await isolated.coordinator.directory();assert.equal(isolated.alerts.length,0);
isolated.reject=true;
isolated.heads.set('alpha',1001);isolated.changes.set('alpha',[{...task('interrupted','alpha-task'),sequence:1001}]);
isolated.heads.set('beta',2001);isolated.changes.set('beta',[{...task('complete','beta-task'),sequence:2001}]);
isolated.pending.set('alpha',[{...approval('alpha-approval'),task_title:'Alpha needs approval'}]);
isolated.pending.set('beta',[{...approval('beta-approval'),request:{owner:'beta'},task_title:'Beta needs approval'}]);
await isolated.coordinator.refresh();
assert.equal(isolated.alerts.filter(item=>item.body==='Background task complete').length,1,'Alpha error must not baseline Beta completion');
assert.equal(isolated.alerts.filter(item=>item.body==='Approval required. Review in Pot.').length,2,'Task error must not block either approval reader');
isolated.client.assistants[1]={...isolated.client.assistants[1],reply_id:'beta-new',reply_count:5};
await isolated.coordinator.directory();assert.equal(isolated.alerts.at(-1).body,'Assistant reply complete','Query error must preserve reply baseline');
isolated.reject=false;await isolated.coordinator.refresh({topics:['task-outcomes']});
assert.equal(isolated.alerts.at(-1).body,'Background task failed or interrupted','Failed read must retain its last successful cursor');
const beforeToken=isolated.calls.length;
await isolated.coordinator.refresh({topics:['tasks','runner','usage']});
assert.equal(isolated.calls.length,beforeToken,'Token/output invalidations must not query notifications');
assert(isolated.calls.some(item=>item.path==='/api/v1/tasks/outcomes?after=1000'),'Only read changes after the 1000-row archive baseline');
let releaseAlpha;isolated.stall=new Promise(resolve=>{releaseAlpha=resolve;});
isolated.heads.set('beta',2002);isolated.changes.get('beta').push({...task('interrupted','beta-fast'),sequence:2002});
isolated.pending.get('alpha').push({...approval('alpha-second'),task_title:'Second approval'});
const stalledRead=isolated.coordinator.refresh();
await new Promise(resolve=>setImmediate(resolve));
assert(isolated.alerts.some(item=>item.title.includes('Second approval')),'A stalled Task read must not block the same owner approval');
assert.equal(isolated.alerts.filter(item=>item.body==='Background task failed or interrupted').length,2,'A stalled owner must not block another owner');
releaseAlpha();await stalledRead;isolated.stall=null;
// Reconnect establishes the current head rather than replaying catch-up.
await isolated.coordinator.refresh({reset:true,space:'space-a'});
const beforeCatchup=isolated.alerts.length;
isolated.heads.set('alpha',1002);isolated.changes.get('alpha').push({...task('complete','offline-task'),sequence:1002});
await isolated.coordinator.directory();assert.equal(isolated.alerts.length,beforeCatchup);
isolated.heads.set('alpha',1003);isolated.changes.get('alpha').push({...task('complete','online-task'),sequence:1003});
await isolated.coordinator.refresh({topics:['task-outcomes']});assert.equal(isolated.alerts.length,beforeCatchup+1);
isolated.coordinator.dispose();

// A new Task can be created and finish between reads. A bounded cursor page
// retains it without any prior active-list observation, including page two.
const paged=isolatedClient();await paged.coordinator.directory();
const oldRequest=paged.client.request;
let pageReads=0;
paged.client.request=async(path,scope)=>{
 if(scope.owner!=='alpha'||!path.startsWith('/api/v1/tasks/outcomes'))return oldRequest(path,scope);
 pageReads++;const after=Number(new URL(path,'http://fixture').searchParams.get('after'));
 return after===1000?{items:[{...task('complete','fast-one'),sequence:1001}],cursor:1001,has_more:true}:
  {items:[{...task('interrupted','fast-two'),sequence:1002}],cursor:1002,has_more:false};
};
await paged.coordinator.refresh({topics:['task-outcomes']});
assert.equal(pageReads,2);assert.equal(paged.alerts.length,2);paged.coordinator.dispose();

// Retire an in-flight old connection before it can install a new baseline or
// deliver an old outcome into the replacement watcher.
const stale=isolatedClient();await stale.coordinator.directory();
let releaseStale,arrivedStale;
const staleStarted=new Promise(resolve=>{arrivedStale=resolve;}),staleResponse=new Promise(resolve=>{releaseStale=resolve;});
const realRequest=stale.client.request;
stale.client.request=(path,scope)=>{
 if(scope.owner==='alpha'&&path.includes('after=1000')){arrivedStale();return staleResponse;}
 return realRequest(path,scope);
};
const oldRead=stale.coordinator.refresh({topics:['task-outcomes']});await staleStarted;
await stale.coordinator.refresh({reset:true,space:'space-a'});
stale.heads.set('alpha',1002);await stale.coordinator.directory();
releaseStale({items:[{...task('complete','stale-task'),sequence:1001}],cursor:1001,has_more:false});await oldRead;
assert.equal(stale.alerts.length,0,'Old connection outcomes cannot cross reset');
stale.heads.set('alpha',1003);stale.changes.set('alpha',[{...task('complete','fresh-task'),sequence:1003}]);
await stale.coordinator.refresh({topics:['task-outcomes']});assert.equal(stale.alerts.length,1);stale.coordinator.dispose();

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

// First-run discovery is independent of event observation and permission. The
// fake atomic claim is shared across clients/Controls, like the durable IDB key.
{
const {claimNotificationOnboarding}=await import('../../web/src/adapters/notification-onboarding.mjs');
assert.equal(await claimNotificationOnboarding({}),false,'blocked persistence skips discovery safely');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function onboardingFixture({permission='default',enabled=true,secure=true,ua='',desktop=false,surface='main',claim,storageError=false}={}){
 let prompts=0,nativeReady=0,finish,failRequest,permissionValue=permission;
 class Permission {static get permission(){return permissionValue;} static requestPermission(){prompts++;return new Promise((resolve,reject)=>{finish=value=>{permissionValue=value;resolve(value);};failRequest=reject;});} constructor(){throw Error('discovery must never send notifications');}}
 const h=new EventTarget(),document=new EventTarget();document.visibilityState='visible';document.hasFocus=()=>true;
 Object.assign(h,{document,isSecureContext:secure,Notification:Permission,navigator:{userAgent:ua},localStorage:{getItem:()=>{if(storageError)throw Error('storage blocked');return String(enabled);}},setTimeout});
 const c={desktop,assistants:[],spaces:[],notificationStatus:async()=>({enabled,permission}),notificationReady:async surface=>{assert.equal(surface,'main');nativeReady++;},notificationView:async()=>{}};
 const manager=createSystemNotifications({client:c,surface,view:()=>({}),open:()=>{},locale:()=> 'en',host:h,onboardingClaim:claim|| (async()=>true)});
 return {manager,client:c,host:h,get prompts(){return prompts;},get nativeReady(){return nativeReady;},finish:value=>finish(value),fail:()=>failRequest(Error('browser failed'))};
}
let claimed=false,claimCount=0;
const once=async()=>{claimCount++;if(claimed)return false;claimed=true;return true;};
const first=onboardingFixture({claim:once}),second=onboardingFixture({claim:once});
assert.equal(first.manager.state.onboarding,false);assert.equal(first.prompts,0,'loading cannot prompt');
first.manager.ready();second.manager.ready();await tick();
assert.equal(Number(first.manager.state.onboarding)+Number(second.manager.state.onboarding),1,'one tab claims discovery');
first.manager.dismissOnboarding();assert.equal(first.manager.state.enabled,true);assert.equal(first.manager.state.permission,'default');
await first.manager.directory();await first.manager.refresh({reset:true});first.manager.ready();first.host.dispatchEvent(new Event('focus'));await tick();
assert.equal(first.manager.state.onboarding,false,'navigation/reconnect/focus cannot redisplay');
const reloaded=onboardingFixture({claim:once});reloaded.manager.ready();await tick();assert.equal(reloaded.manager.state.onboarding,false,'refresh/new Control shares the permanent claim');
for(const item of [first,second,reloaded])item.manager.dispose();
assert.equal(claimCount,3);
for(const options of [{permission:'granted'},{permission:'denied'},{enabled:false},{secure:false},{ua:'iPhone'},{ua:'Android Mobile'}]){
 const f=onboardingFixture({...options,claim:async()=>{throw Error('ineligible client claimed onboarding');}});f.manager.ready();await tick();assert(!f.manager.state.onboarding);assert.equal(f.prompts,0);f.manager.dispose();
}
for(const result of ['granted','denied','default','error']){
 const f=onboardingFixture();f.manager.ready();await tick();assert(f.manager.state.onboarding);
 const pending=f.manager.authorize();assert.equal(f.prompts,1,'request invoked before authorize returns / any await');
 assert.equal(f.manager.authorize(),pending,'double click joins pending request');assert(f.manager.state.authorizing);
 assert(!f.manager.state.onboarding);
 if(result==='error'){f.fail();await assert.rejects(pending,/browser failed/);assert(f.manager.state.error);}
 else{f.finish(result);await pending;assert.equal(f.manager.state.permission,result);}
 f.host.dispatchEvent(new Event('focus'));f.manager.ready();await tick();assert(!f.manager.state.onboarding);assert(!f.manager.state.authorizing);
 if(result==='default'||result==='error'){const manual=f.manager.authorize();assert.equal(f.prompts,2,'manual settings retry remains available');f.finish('granted');await manual;}
 f.manager.dispose();
}
const restricted=onboardingFixture({storageError:true});restricted.manager.ready();await tick();assert(restricted.manager.state.onboarding,'localStorage restrictions do not block an IDB claim');restricted.manager.dispose();
const noStorage=onboardingFixture({claim:async()=>{throw Error('IDB denied');}});noStorage.manager.ready();await tick();assert(!noStorage.manager.state.onboarding);noStorage.manager.dispose();
const nativeDiscovery=onboardingFixture({desktop:true});await tick();assert.equal(nativeDiscovery.nativeReady,0);nativeDiscovery.manager.ready();nativeDiscovery.manager.ready();await tick();assert.equal(nativeDiscovery.nativeReady,1);assert(!nativeDiscovery.manager.state.onboarding);nativeDiscovery.manager.dispose();
const trayDiscovery=onboardingFixture({desktop:true,surface:'tray'});trayDiscovery.manager.ready();await tick();assert.equal(trayDiscovery.nativeReady,0,'tray mount never starts native authorization');trayDiscovery.manager.dispose();
const deferred=onboardingFixture({desktop:true});let readinessCalls=0;
deferred.client.notificationReady=async()=>({deferred:++readinessCalls===1});deferred.manager.ready();await tick();assert.equal(readinessCalls,1,'hidden native window does not spin');deferred.host.dispatchEvent(new Event('focus'));await tick();assert.equal(readinessCalls,2);deferred.host.dispatchEvent(new Event('focus'));await tick();assert.equal(readinessCalls,2);deferred.manager.dispose();
let releaseDiscovery;const late=onboardingFixture({claim:()=>new Promise(resolve=>{releaseDiscovery=resolve;})});late.manager.ready();const pendingManual=late.manager.authorize();releaseDiscovery(true);await tick();assert(!late.manager.state.onboarding,'manual request racing a pending claim cannot show the card');late.finish('default');await pendingManual;late.manager.dispose();
const offDuringClaim=onboardingFixture();offDuringClaim.host.localStorage.setItem=()=>{};offDuringClaim.manager.ready();await tick();assert(offDuringClaim.manager.state.onboarding);await offDuringClaim.manager.setEnabled(false);assert(!offDuringClaim.manager.state.onboarding);await offDuringClaim.manager.authorize();assert.equal(offDuringClaim.prompts,0,'off preference prevents stale controls from authorizing');offDuringClaim.manager.dispose();
assert.equal(browserNotificationPermission({isSecureContext:true,navigator:{userAgent:'iPhone'}}),'ios','iOS explanation remains specific when Notification is absent');
assert.equal(browserNotificationPermission({isSecureContext:true,Notification:function(){}}),'unsupported','object presence alone is insufficient');
assert.equal(browserNotificationPermission({isSecureContext:true,Notification:Object.assign(function(){},{requestPermission(){}}),navigator:{platform:'MacIntel',maxTouchPoints:5}}),'ios','iPad desktop UA still requires mobile delivery');
console.log('First-run notification eligibility, dismissal, gesture, pending/error, persistence fallback and native readiness checks passed');

}
