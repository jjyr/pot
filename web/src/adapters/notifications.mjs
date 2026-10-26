import {claimNotificationOnboarding} from './notification-onboarding.mjs';
import {notificationText} from '../state/notifications.mjs';
import {notificationCapabilities,notificationPermission,createPushInstallation} from './pwapush.mjs';
import {notificationInstallation,validNotificationTarget} from './notification-installation.mjs';

const preferenceKey='pot-system-notifications-browser-v1';
const failureText=reason=>reason?.message||String(reason??'Unknown notification failure');
export const browserNotificationPermission=notificationPermission;

// The backend source selects business events; browser services own per-device
// receipts and reading suppression. This adapter owns permission and presentation.
export function createSystemNotifications({client,surface,view,open,locale,host=window,onboardingClaim=claimNotificationOnboarding}){
  const listeners=new Set();let disposed=false,enabled=true,permission='loading',error='',generation=0,ready=false,awaitingDirectory=true,pendingClick=null;
  let onboardingAttempted=false,onboarding=false,authorization=null,authorizing=false,statusEpoch=0,initialized=null,registered=false,loop=null,leaseTimer=null,synchronizing=Promise.resolve(),suspended=false,leaseSequence=0,lastLease='',lastLeaseAt=0;
  let lifetime=new AbortController();
  const session=host.crypto.randomUUID();
  const installation=!client.desktop&&client.transport?createPushInstallation({client,host,locale}):null;
  const emit=()=>listeners.forEach(listener=>listener());
  const capability=()=>client.desktop?{channel:'native'}:notificationCapabilities(host);
  const destination=event=>{const space=(client.spaces||[]).find(space=>space.control_id===event.control&&space.connected!==false);return space?{...event,space:space.id}:event};
  const context=()=>({...view(),focused:host.document.visibilityState==='visible'&&host.document.hasFocus(),locale:locale()});
  const fail=reason=>{if(!disposed&&reason?.name!=='AbortError'){error=failureText(reason);emit()}};
  const intended=()=>{try{return host.localStorage.getItem(preferenceKey)!=='false'}catch{return enabled}};
  const current=ticket=>!disposed&&!suspended&&ticket===lifetime&&!ticket.signal.aborted;
  const options=ticket=>({signal:ticket.signal,valid:()=>current(ticket)&&intended()===enabled});
  const stopDelivery=()=>{loop?.abort();loop=null;registered=false;lastLease='';lastLeaseAt=0;if(leaseTimer){host.clearInterval(leaseTimer);leaseTimer=null}};
  function retire(){lifetime.abort();lifetime=new AbortController();initialized=null;stopDelivery()}
  async function initialize(ticket=lifetime){
    if(!installation||!capability().channel||!current(ticket))return;
    if(!initialized){const pending=installation.initialize(capability().channel,enabled,options(ticket));initialized=pending;pending.catch(()=>{if(initialized===pending)initialized=null})}
    return initialized;
  }
  async function report(ticket=lifetime){
    if(!current(ticket))return;
    try{
      if(client.desktop){await client.notificationView(surface,context());return}
      if(!registered||!enabled||permission!=='granted')return;
      if(notificationPermission(host)!=='granted'){stopDelivery();await status();return}
      const {locale:ignored,...value}=context();
      const space=(client.spaces||[]).find(space=>space.id===value.space);
      const reading={...value,space:space?.control_id||value.space},signature=JSON.stringify(reading),now=Date.now();
      if(signature===lastLease&&now-lastLeaseAt<10000)return;
      lastLease=signature;lastLeaseAt=now;
      const result=await installation.lease(session,{...reading,sequence:++leaseSequence},options(ticket));
      if(!current(ticket))return;if(result===false){stopDelivery();emit()}
    }catch(reason){if(current(ticket)){fail(reason);if([401,403,409,410].includes(reason.status))stopDelivery()}}
  }
  function backoff(signal,delay){return new Promise(resolve=>{
    let timer;const finish=()=>{host.clearTimeout(timer);signal.removeEventListener('abort',finish);resolve()};
    if(signal.aborted)return resolve();timer=host.setTimeout(finish,delay);signal.addEventListener('abort',finish,{once:true});
  })}
  async function consume(signal,ticket,establish=false){
    let delay=250,baselined=!establish;
    while(current(ticket)&&!signal.aborted&&registered&&enabled&&intended()&&notificationPermission(host)==='granted'){
      try{
        const operation={signal,valid:()=>current(ticket)&&!signal.aborted&&enabled&&notificationPermission(host)==='granted'};
        if(!baselined){await installation.baseline(session,operation);if(!current(ticket)||signal.aborted)return;baselined=true}
        const {delivery}=await installation.next(signal,session);
        if(!current(ticket)||signal.aborted||!enabled)return;
        if(!intended()){stopDelivery();await status();return}
        if(notificationPermission(host)!=='granted'){stopDelivery();await status();return}
        delay=250;if(error){error='';emit()}if(!delivery)continue;
        const event=delivery.event;
        if(!validNotificationTarget(event)||event.control!==installation.device.control){await installation.receipt(delivery,'failed',operation);continue}
        let notification;
        try{const text=notificationText(event,locale());notification=new host.Notification(text.title,{body:text.body,tag:event.id,icon:'/assets/PotCompanion.png'})}
        catch(reason){const definite=['TypeError','NotAllowedError','SecurityError','NotSupportedError'].includes(reason?.name);await installation.receipt(delivery,definite?'failed':'unknown',operation);if(current(ticket))fail(reason);continue}
        notification.onclick=()=>{notification.close();host.focus();Promise.resolve(open(destination(event))).catch(fail)};
        await installation.receipt(delivery,'accepted',operation);
      }catch(reason){
        if(signal.aborted||!current(ticket))return;fail(reason);
        if([401,403,409,410].includes(reason.status)||notificationPermission(host)!=='granted'){stopDelivery();return}
        // Retain this receiver lock, lease and baseline across transient IO
        // failures. Sending uncertainty is handled by server receipts, never by
        // replaying a displayed event or switching channel.
        await backoff(signal,delay);delay=Math.min(delay*2,15000);
      }
    }
  }
  function synchronize(){const ticket=lifetime;synchronizing=synchronizing.catch(()=>{}).then(()=>sync(ticket));return synchronizing}
  async function sync(ticket){
    if(!current(ticket)||client.desktop||!installation||!ready||awaitingDirectory)return;
    const channel=capability().channel,intent=enabled,authorization=permission;
    const valid=()=>current(ticket)&&!awaitingDirectory&&enabled===intent&&intended()===intent&&permission===authorization&&capability().channel===channel;
    const operation={signal:ticket.signal,valid};
    if(!channel){stopDelivery();return}
    await initialize(ticket);if(!valid())return;
    if(!intent){stopDelivery();await installation.disable(operation);return}
    if(!['granted','default','denied'].includes(authorization)){stopDelivery();return}
    if(channel==='push'&&!installation.subscribed){stopDelivery();await installation.disable(operation);if(valid())emit();return}
    const result=await installation.register(channel,intent,authorization,operation);if(!valid())return;
    if(result===false){stopDelivery();emit();return}
    if(authorization!=='granted'){stopDelivery();return}
    if(!registered){
      if(channel!=='local'){await installation.baseline(session,operation);if(!valid())return}registered=true;
      await report(ticket);if(!valid()||!registered)return;
      leaseTimer=host.setInterval(()=>report(ticket),10000);
      if(channel==='local'){
        loop=new AbortController();const signal=loop.signal;
        const receive=establish=>consume(signal,ticket,establish);
        const acquire=async()=>{
          if(!host.navigator.locks)return receive(true);
          const name='pot-notification-receiver:'+installation.device.id;let acquired=false;
          await host.navigator.locks.request(name,{ifAvailable:true},async lock=>{if(!lock||!valid()||signal.aborted)return;acquired=true;await receive(true)});
          // A standby page joins an already-live installation. Taking over its
          // lock must retain queued current work rather than reset the baseline.
          if(!acquired&&valid()&&!signal.aborted)await host.navigator.locks.request(name,{signal},()=>receive(false));
        };
        const work=acquire();
        Promise.resolve(work).catch(reason=>{if(!signal.aborted&&current(ticket))fail(reason)});
      }
    }
  }
  async function status(){
    if(disposed)return;const ticket=++statusEpoch;let nextEnabled=enabled,nextPermission,nextError='';
    try{
      if(client.desktop){const value=await client.notificationStatus();nextEnabled=value.enabled;nextPermission=value.permission}
      else{try{nextEnabled=host.localStorage.getItem(preferenceKey)!=='false'}catch{}nextPermission=notificationPermission(host)}
    }catch(reason){nextPermission='unavailable';nextError=failureText(reason)}
    if(disposed||ticket!==statusEpoch)return;
    if(enabled!==nextEnabled||permission!==nextPermission)retire();
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
    const ticket=lifetime;if(client.desktop||!host.indexedDB||!installation||!current(ticket))return;await initialize(ticket);if(!current(ticket))return;
    const event=await notificationInstallation(async store=>{const device=await store.get('device'),pending=await store.get('click');if(!current(ticket))return null;store.remove('click');return pending?.device===device?.id&&device?.enabled?pending.event:null},host);
    if(current(ticket)&&event)click(event);
  }
  const focus=()=>{report();status()};
  const reportEvent=()=>report();
  const suspend=()=>{if(client.desktop||disposed)return;suspended=true;retire()};
  const resume=()=>{if(client.desktop||disposed)return;suspended=false;status()};
  const storage=event=>{if(event.key===preferenceKey)status()};
  const localeChanged=()=>{report();synchronize().catch(fail)};
  host.addEventListener('pagehide',suspend);host.addEventListener('pageshow',resume);host.addEventListener('offline',suspend);host.addEventListener('online',resume);host.document.addEventListener('freeze',suspend);host.document.addEventListener('resume',resume);
  host.addEventListener('focus',focus);host.addEventListener('blur',reportEvent);host.addEventListener('storage',storage);host.addEventListener('pot:locale',localeChanged);host.document.addEventListener('visibilitychange',focus);
  host.navigator?.serviceWorker?.addEventListener?.('message',swMessage);
  status();
  return {
    get epoch(){return generation},
    get state(){return {enabled,permission,error,onboarding,authorizing,channel:capability().channel,needsSubscription:capability().channel==='push'&&permission==='granted'&&!installation?.subscribed}},
    subscribe(listener){listeners.add(listener);return ()=>listeners.delete(listener)},
    async setEnabled(value){
      if(disposed)return;if(client.desktop)await client.setNotificationsEnabled(value);else host.localStorage.setItem(preferenceKey,String(value));
      if(disposed)return;statusEpoch++;enabled=value;retire();const ticket=lifetime;if(!value)onboarding=false;emit();
      // Off is durable user intent, including if the page freezes during this
      // transaction. A subsequent explicit on invalidates this write.
      if(!client.desktop&&!value)await installation?.intent(false,{valid:()=>!intended()});if(!current(ticket))return;await synchronize();maybeOnboard();
    },
    ready(){if(ready)return;ready=true;maybeOnboard();synchronize().catch(fail);takeClick().catch(fail)},
    dismissOnboarding(){onboarding=false;emit()},
    authorize(){
      if(authorization)return authorization;if(!enabled||disposed||suspended)return Promise.resolve();
      const ticket=lifetime;
      onboarding=false;error='';authorizing=true;let request;
      // Invoke permission before any await; denial never changes channel.
      try{if(client.desktop)request=client.requestNotificationPermission();else if(notificationPermission(host)==='default')request=host.Notification.requestPermission()}catch(reason){request=Promise.reject(reason)}
      emit();authorization=Promise.resolve(request).then(async()=>{
        if(!current(ticket)||!enabled)return;
        if(!client.desktop&&capability().channel==='push'&&notificationPermission(host)==='granted'){await initialize(ticket);if(!current(ticket)||!enabled)return;await installation.subscribe(options(ticket));if(!current(ticket)||!enabled)return}
        await status();
      },async reason=>{await status();fail(reason);throw reason}).catch(reason=>{fail(reason);throw reason}).finally(()=>{authorization=null;authorizing=false;emit()});return authorization;
    },
    refresh({reset=false}={}){if(reset){generation++;awaitingDirectory=true;retire()}return Promise.resolve()},
    directory({epoch=generation}={}){if(epoch!==generation||disposed)return Promise.resolve();awaitingDirectory=false;openPending();return synchronize().catch(fail)},
    report,
    dispose(){disposed=true;retire();stopClick();stopPreference();listeners.clear();host.removeEventListener('pagehide',suspend);host.removeEventListener('pageshow',resume);host.removeEventListener('offline',suspend);host.removeEventListener('online',resume);host.document.removeEventListener('freeze',suspend);host.document.removeEventListener('resume',resume);host.removeEventListener('focus',focus);host.removeEventListener('blur',reportEvent);host.removeEventListener('storage',storage);host.removeEventListener('pot:locale',localeChanged);host.document.removeEventListener('visibilitychange',focus);host.navigator?.serviceWorker?.removeEventListener?.('message',swMessage)},
  };
}
