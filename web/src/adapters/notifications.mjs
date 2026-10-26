import {claimNotificationOnboarding} from './notification-onboarding.mjs';
import {notificationText} from '../state/notifications.mjs';
import {notificationCapabilities,notificationPermission,createPushInstallation} from './pwapush.mjs';
import {notificationInstallation,validNotificationTarget} from './notification-installation.mjs';

const preferenceKey='pot-system-notifications-browser-v1';
const failureText=reason=>reason?.message||String(reason??'Unknown notification failure');
export const browserNotificationPermission=notificationPermission;

// Business event selection, device deduplication and foreground suppression live
// in the backend hub. This adapter owns permission, host presentation and leases.
export function createSystemNotifications({client,surface,view,open,locale,host=window,onboardingClaim=claimNotificationOnboarding}){
  const listeners=new Set();let disposed=false,enabled=true,permission='loading',error='',generation=0,ready=false,awaitingDirectory=true,pendingClick=null;
  let onboardingAttempted=false,onboarding=false,authorization=null,authorizing=false,statusEpoch=0,initialized=null,registered=false,loop=null,leaseTimer=null,synchronizing=Promise.resolve(),suspended=false,leaseSequence=0,lastLease='',lastLeaseAt=0;
  const session=host.crypto.randomUUID();
  const installation=!client.desktop&&client.transport?createPushInstallation({client,host,locale}):null;
  const emit=()=>listeners.forEach(listener=>listener());
  const capability=()=>client.desktop?{channel:'native'}:notificationCapabilities(host);
  const destination=event=>{const space=(client.spaces||[]).find(space=>space.control_id===event.control&&space.connected!==false);return space?{...event,space:space.id}:event};
  const context=()=>({...view(),focused:host.document.visibilityState==='visible'&&host.document.hasFocus(),locale:locale()});
  const fail=reason=>{if(!disposed){error=failureText(reason);emit()}};
  const stopDelivery=()=>{loop?.abort();loop=null;registered=false;lastLease='';lastLeaseAt=0;if(leaseTimer){host.clearInterval(leaseTimer);leaseTimer=null}};
  async function initialize(){
    if(!installation||!capability().channel)return;
    if(!initialized)initialized=installation.initialize(capability().channel,enabled).catch(error=>{initialized=null;throw error});
    return initialized;
  }
  async function report(){
    if(disposed)return;
    try{
      if(client.desktop){await client.notificationView(surface,context());return}
      if(!registered)return;
      const {locale:ignored,...value}=context();
      const space=(client.spaces||[]).find(space=>space.id===value.space);
      const reading={...value,space:space?.control_id||value.space},signature=JSON.stringify(reading),now=Date.now();
      if(signature===lastLease&&now-lastLeaseAt<10000)return;
      lastLease=signature;lastLeaseAt=now;
      if(await installation.lease(session,{...reading,sequence:++leaseSequence})===false){stopDelivery();emit()}

    }catch(reason){fail(reason)}
  }
  async function consume(signal){
    while(!disposed&&!signal.aborted&&registered){
      try{
        const {delivery}=await installation.next(signal,session);if(!delivery)continue;
        if(signal.aborted||disposed){await installation.receipt(delivery,'unknown');return}
        if(!enabled||notificationPermission(host)!=='granted'){await installation.receipt(delivery,'failed');return}
        const event=delivery.event;
        if(!validNotificationTarget(event)||event.control!==installation.device.control){await installation.receipt(delivery,'failed');continue}
        let notification;
        try{const text=notificationText(event,locale());notification=new host.Notification(text.title,{body:text.body,tag:event.id,icon:'/assets/PotCompanion.png'})}
        catch(reason){const definite=['TypeError','NotAllowedError','SecurityError','NotSupportedError'].includes(reason?.name);await installation.receipt(delivery,definite?'failed':'unknown');fail(reason);continue}
        notification.onclick=()=>{notification.close();host.focus();Promise.resolve(open(destination(event))).catch(fail)};
        await installation.receipt(delivery,'accepted');
      }catch(reason){if(signal.aborted||disposed)return;fail(reason);stopDelivery();return}
    }
  }
  function synchronize(){synchronizing=synchronizing.catch(()=>{}).then(sync);return synchronizing}
  async function sync(){
    if(disposed||client.desktop||!installation||!ready||awaitingDirectory||suspended)return;
    const channel=capability().channel;
    if(!channel){stopDelivery();return}
    await initialize();
    if(disposed||awaitingDirectory)return;
    if(!enabled){stopDelivery();await installation.disable();return}
    if(!['granted','default','denied'].includes(permission)){stopDelivery();return}
    if(channel==='push'&&!installation.subscribed){stopDelivery();await installation.disable();emit();return}
    if(await installation.register(channel,enabled,permission)===false){stopDelivery();emit();return}
    if(permission!=='granted'){stopDelivery();return}
    if(!registered){
      await installation.baseline(session);registered=true;
      await report();leaseTimer=host.setInterval(report,10000);
      if(channel==='local'){
        loop=new AbortController();const signal=loop.signal;
        // One receiver per installation avoids occupying an extra HTTP stream
        // in every tab. This lock owns transport IO; hub receipts still decide
        // event policy and deduplication.
        const receive=()=>consume(signal);
        const work=host.navigator.locks?host.navigator.locks.request('pot-notification-receiver:'+installation.device.id,{signal},receive):receive();
        Promise.resolve(work).catch(reason=>{if(!signal.aborted)fail(reason)});
      }
    }
  }
  async function status(){
    const ticket=++statusEpoch;let nextEnabled=enabled,nextPermission,nextError='';
    try{
      if(client.desktop){const value=await client.notificationStatus();nextEnabled=value.enabled;nextPermission=value.permission}
      else{try{nextEnabled=host.localStorage.getItem(preferenceKey)!=='false'}catch{}nextPermission=notificationPermission(host)}
    }catch(reason){nextPermission='unavailable';nextError=failureText(reason)}
    if(disposed||ticket!==statusEpoch)return;
    enabled=nextEnabled;permission=nextPermission;error=nextError;if(!enabled||permission!=='default')onboarding=false;
    emit();maybeOnboard();await synchronize().catch(fail);
  }
  async function maybeOnboard(){
    if(!ready||disposed||onboardingAttempted||surface!=='main'||host.document.visibilityState!=='visible')return;
    if(client.desktop){onboardingAttempted=true;try{const result=await client.notificationReady(surface);if(result?.deferred){onboardingAttempted=false;return}await status()}catch(reason){fail(reason)}return}
    // Preserve the existing desktop introduction; mobile installation guidance
    // remains in the same settings entry, and never requests permission itself.
    if(!enabled||authorizing||permission!=='default'||capability().channel!=='local')return;
    onboardingAttempted=true;const claimed=await onboardingClaim(host).catch(()=>false);
    if(claimed&&!disposed&&enabled&&!authorizing&&notificationPermission(host)==='default'){onboarding=true;emit()}
  }
  function openPending(){if(!pendingClick||awaitingDirectory||disposed)return;const value=pendingClick;pendingClick=null;Promise.resolve(open(destination(value))).catch(fail)}
  const click=event=>{if(validNotificationTarget(event)){pendingClick=event;openPending()}};
  const stopClick=surface==='main'?client.onNotificationClick?.(click)||(()=>{}):()=>{};
  const stopPreference=client.onNotificationPreference?.(()=>status())||(()=>{});
  const swMessage=event=>{if(event.data?.type==='pot:notification-click')takeClick().catch(fail)};
  async function takeClick(){
    if(client.desktop||!host.indexedDB||!installation)return;await initialize();
    const event=await notificationInstallation(async store=>{const device=await store.get('device'),pending=await store.get('click');store.remove('click');return pending?.device===device?.id&&device?.enabled?pending.event:null},host);
    if(event)click(event);
  }
  const focus=()=>{report();status()};
  const suspend=()=>{if(client.desktop)return;report();suspended=true;stopDelivery()};
  const resume=()=>{if(client.desktop)return;suspended=false;status()};
  const storage=event=>{if(event.key===preferenceKey)status()};
  const localeChanged=()=>{report();synchronize().catch(fail)};
  host.addEventListener('pagehide',suspend);host.addEventListener('pageshow',resume);host.addEventListener('offline',suspend);host.addEventListener('online',resume);host.document.addEventListener('freeze',suspend);host.document.addEventListener('resume',resume);
  host.addEventListener('focus',focus);host.addEventListener('blur',report);host.addEventListener('storage',storage);host.addEventListener('pot:locale',localeChanged);host.document.addEventListener('visibilitychange',focus);
  host.navigator?.serviceWorker?.addEventListener?.('message',swMessage);
  status();
  return {
    get epoch(){return generation},
    get state(){return {enabled,permission,error,onboarding,authorizing,channel:capability().channel,needsSubscription:capability().channel==='push'&&permission==='granted'&&!installation?.subscribed}},
    subscribe(listener){listeners.add(listener);return ()=>listeners.delete(listener)},
    async setEnabled(value){
      if(client.desktop)await client.setNotificationsEnabled(value);else host.localStorage.setItem(preferenceKey,String(value));
      statusEpoch++;enabled=value;if(!value)onboarding=false;emit();await synchronize();maybeOnboard();
    },
    ready(){if(ready)return;ready=true;maybeOnboard();synchronize().catch(fail);takeClick().catch(fail)},
    dismissOnboarding(){onboarding=false;emit()},
    authorize(){
      if(authorization)return authorization;if(!enabled)return Promise.resolve();
      onboarding=false;error='';authorizing=true;let request;
      // Invoke permission before any await; denial never changes channel.
      try{if(client.desktop)request=client.requestNotificationPermission();else if(notificationPermission(host)==='default')request=host.Notification.requestPermission()}catch(reason){request=Promise.reject(reason)}
      emit();authorization=Promise.resolve(request).then(async()=>{
        if(!client.desktop&&capability().channel==='push'&&notificationPermission(host)==='granted'){await initialize();await installation.subscribe()}
        await status();
      },async reason=>{await status();fail(reason);throw reason}).catch(reason=>{fail(reason);throw reason}).finally(()=>{authorization=null;authorizing=false;emit()});return authorization;
    },
    refresh({reset=false}={}){if(reset){generation++;awaitingDirectory=true;stopDelivery()}return Promise.resolve()},
    directory({epoch=generation}={}){if(epoch!==generation||disposed)return Promise.resolve();awaitingDirectory=false;openPending();return synchronize().catch(fail)},
    report,
    dispose(){disposed=true;stopDelivery();stopClick();stopPreference();listeners.clear();host.removeEventListener('pagehide',suspend);host.removeEventListener('pageshow',resume);host.removeEventListener('offline',suspend);host.removeEventListener('online',resume);host.document.removeEventListener('freeze',suspend);host.document.removeEventListener('resume',resume);host.removeEventListener('focus',focus);host.removeEventListener('blur',report);host.removeEventListener('storage',storage);host.removeEventListener('pot:locale',localeChanged);host.document.removeEventListener('visibilitychange',focus);host.navigator?.serviceWorker?.removeEventListener?.('message',swMessage)},
  };
}
