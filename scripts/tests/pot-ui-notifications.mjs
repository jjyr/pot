import assert from 'node:assert/strict';
import {createSystemNotifications,browserNotificationPermission} from '../../web/src/adapters/notifications.mjs';
import {notificationCapabilities} from '../../web/src/adapters/pwapush.mjs';
import {notificationText} from '../../web/src/state/notifications.mjs';

const local={isSecureContext:true,navigator:{userAgent:'Desktop'},Notification:class{static permission='default';static requestPermission(){}},PushManager:class{},ServiceWorkerRegistration:class{showNotification(){}},matchMedia:()=>({matches:true})};
local.navigator.serviceWorker={ready:Promise.reject(Error('must never touch Push on a local-capable device'))};local.navigator.serviceWorker.ready.catch(()=>{});
for(const permission of ['default','denied','granted']){local.Notification.permission=permission;assert.equal(notificationCapabilities(local).channel,'local');assert.equal(browserNotificationPermission(local),permission)}
const phone={...local,navigator:{...local.navigator,userAgent:'iPhone',standalone:true}};
assert.equal(notificationCapabilities(phone).channel,'push');
assert.equal(notificationCapabilities({...phone,navigator:{...phone.navigator,standalone:false},matchMedia:()=>({matches:false})}).limitation,'iosInstall');
assert.equal(notificationCapabilities({...phone,PushManager:undefined}).channel,null);
assert.equal(notificationCapabilities({...local,Notification:undefined}).channel,null);
assert.equal(notificationCapabilities({...local,isSecureContext:false}).limitation,'insecure');
assert.equal(notificationText({kind:'reply',name:'Alpha',preview:'完成'},'zh-CN').body,'完成');
assert.equal(notificationText({kind:'approval',name:'Build',body_key:'notifications.approvalRequired'},'zh-CN').body,'需要你的审批，请在 Pot 中查看');

const {claimNotificationOnboarding}=await import('../../web/src/adapters/notification-onboarding.mjs');
assert.equal(await claimNotificationOnboarding({}),false,'blocked persistence skips discovery safely');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function onboardingFixture({permission='default',enabled=true,secure=true,ua='',desktop=false,surface='main',claim,storageError=false}={}){
 let prompts=0,nativeReady=0,finish,failRequest,permissionValue=permission;
 class Permission {static get permission(){return permissionValue;} static requestPermission(){prompts++;return new Promise((resolve,reject)=>{finish=value=>{permissionValue=value;resolve(value);};failRequest=reject;});} constructor(){throw Error('discovery must never send notifications');}}
 const h=new EventTarget(),document=new EventTarget();document.visibilityState='visible';document.hasFocus=()=>true;
 Object.assign(h,{document,isSecureContext:secure,Notification:Permission,navigator:{userAgent:ua},localStorage:{getItem:()=>{if(storageError)throw Error('storage blocked');return String(enabled);}},setTimeout,crypto:globalThis.crypto});
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
assert.equal(browserNotificationPermission({isSecureContext:true,navigator:{userAgent:'iPhone'}}),'iosInstall','iOS explanation remains specific when Notification is absent');
assert.equal(browserNotificationPermission({isSecureContext:true,Notification:function(){}}),'unsupported','object presence alone is insufficient');
assert.equal(browserNotificationPermission({isSecureContext:true,Notification:Object.assign(function(){},{requestPermission(){}}),navigator:{platform:'MacIntel',maxTouchPoints:5}}),'iosInstall','iPad desktop UA still requires mobile delivery');
console.log('First-run notification eligibility, dismissal, gesture, pending/error, persistence fallback and native readiness checks passed');
