import {createInstaller, isInstalled} from '/install.js';
let module, page='chat', task='', snapshot, notice='', sending=false, refreshing=false, accepted;
let scope='active', query='', offset=0, nextOffset=null, pushStatus;
const initialRoute=new URLSearchParams(location.search);
task=initialRoute.get('task')||'';page=task?'task':(['chat','tasks','sandbox','more','usage','schedules'].includes(initialRoute.get('page'))?initialRoute.get('page'):'chat');
function navigate(){history.pushState({page,task,scope,query,offset},'',task?'/?task='+encodeURIComponent(task):'/?page='+page);}
const el=id=>document.getElementById(id);
function initialLocale(){try{const saved=localStorage.getItem('pot-locale');if(['en','zh-CN'].includes(saved))return saved;}catch{}for(const l of navigator.languages||[]){if(l.toLowerCase().startsWith('zh'))return 'zh-CN';if(l.toLowerCase().startsWith('en'))return 'en';}return 'en';}
let language=initialLocale();
const catalogs=await fetch('/locales.json',{cache:'no-store'}).then(r=>r.json());
const t=key=>catalogs[language]?.[key]||catalogs.en[key]||key;
const installer=createInstaller(t,()=>{el('install')&&(el('install').textContent=t(isInstalled()?'mobile.installed':'mobile.install'));refresh();});
el('install').textContent=t(isInstalled()?'mobile.installed':'mobile.install');
el('install').onclick=()=>installer.open();
const decode=s=>Uint8Array.from(atob(s.replace(/-/g,'+').replace(/_/g,'/')),c=>c.charCodeAt(0));
const encode=b=>btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
async function api(path,value){
  const r=await fetch(path,{credentials:'same-origin',cache:'no-store',...(value===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(value)})});
  if(r.status===401&&module){location.reload();throw Error('session_expired');}
  const body=await r.json();if(!r.ok)throw Error(body.code||String(r.status));return body;
}
function options(value){const p=value.publicKey;p.challenge=decode(p.challenge);if(p.user)p.user.id=decode(p.user.id);for(const key of ['allowCredentials','excludeCredentials'])if(p[key])for(const c of p[key])c.id=decode(c.id);return value;}
function credential(c){
  if(!c)throw Error('Verification cancelled');
  const result={id:c.id,rawId:encode(c.rawId),type:c.type,extensions:c.getClientExtensionResults(),response:{clientDataJSON:encode(c.response.clientDataJSON)}};
  for(const k of ['attestationObject','authenticatorData','signature','userHandle'])if(c.response[k])result.response[k]=encode(c.response[k]);
  if(c.response.getTransports)result.response.transports=c.response.getTransports();return result;
}
async function refresh(){
  if(!module||refreshing||document.visibilityState==='hidden')return;refreshing=true;
  const route=task, requestedPage=page, requestedQuery=`${scope}:${query}:${offset}`;
  try{
    const current=await api(route?`/api/v1/tasks/${encodeURIComponent(route)}`:'/api/v1/snapshot');
    if(route!==task||requestedPage!==page)return;
    snapshot=current;
    const receipt=JSON.parse(module.reconcile_send(JSON.stringify(current)));
    if(receipt){accepted=receipt;notice=t('mobile.shell.received');}
    const data={connected:true,reduce_motion:matchMedia('(prefers-reduced-motion: reduce)').matches,page,conversation:task||'main',locale:language,notice,snapshot,accepted,installed:isInstalled(),dark:matchMedia('(prefers-color-scheme: dark)').matches};
    if(page==='tasks'){const result=await api(`/api/v1/tasks/query?scope=${scope}&q=${encodeURIComponent(query)}&offset=${offset}&limit=20`);data.tasks=result.items||[];nextOffset=result.next_offset;data.scope=scope;}
    if(page==='sandbox'){const result=await api('/api/v1/sandboxes');data.sandboxes=result.items||result.sandboxes||[];}
    if(page==='usage'){data.usage=await api('/api/v1/usage/summary?days=7&offset=0&timezone='+encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone));}
    if(page==='schedules'){data.schedules=await api('/api/v1/schedules');}
    if(route!==task||requestedPage!==page||requestedQuery!==`${scope}:${query}:${offset}`)return;
    module.update_view(JSON.stringify(data));
  }catch(e){notice=t('mobile.shell.connection-unavailable')+e.message;module.update_view(JSON.stringify({connected:false,page,conversation:task||'main',locale:language,notice,snapshot,accepted}));}
  finally{refreshing=false;}
}
async function start(){
  pushStatus=await api('/access/status');el('enroll').disabled=!pushStatus.enrollment_open;
  if(pushStatus.authenticated&&!module){
    module=await import('/pkg/pot_mobile.js');await module.default();module.run();el('auth').remove();
    history.replaceState({page,task,scope,query,offset},'');
    await refresh();setInterval(refresh,1500);
    setInterval(()=>{if(pushStatus.push_available)api('/access/push/heartbeat',{visible:document.visibilityState==='visible'&&page==='chat'}).catch(()=>{});},10000);
  }
}
async function sendPending(){
  const sent=JSON.parse(module.pending_send());
  if(!sent)return;
  const path=sent.conversation_id==='main'?'/api/v1/messages':`/api/v1/tasks/${encodeURIComponent(sent.conversation_id)}/messages`;
  const result=await api(path,sent);
  const receipt=JSON.parse(module.accept_send(sent.request_id,JSON.stringify(result)));
  if(receipt)accepted=receipt;notice=t('mobile.shell.received');
}
window.potAction=async(action,value)=>{
  const write=action==='send'||action==='retry';
  if(write&&sending)return;
  if(write)sending=true;
  try{
    if(action==='install')await installer.open();
    if(action==='page'){page=value;task='';navigate();}
    if(action==='task'){task=value;page='task';navigate();}
    if(action==='filter'){scope=value;offset=0;}
    if(action==='search'){query=value;offset=0;}
    if(action==='next'&&nextOffset!=null)offset=nextOffset;
    if(action==='previous')offset=Math.max(0,offset-20);
    if(action==='language'){language=language==='en'?'zh-CN':'en';try{localStorage.setItem('pot-locale',language);}catch{}if(pushStatus.subscription?.enabled)await api('/access/push/preferences',{locale:language});}
    if(action==='logout'){await api('/access/logout',{});const reg=await navigator.serviceWorker?.getRegistration();await (await reg?.pushManager.getSubscription())?.unsubscribe();location.reload();return;}
    if(action==='send'){
      if(JSON.parse(module.pending_send()))throw Error(t('mobile.shell.resolve-the-previous-send-using-retry-first'));
      module.begin_send(task||'main',value,crypto.randomUUID());
      notice=t('mobile.shell.sending');await sendPending();
    }
    if(action==='retry')await sendPending();
    if(action==='stop'){
      const target=snapshot?.turn?.user_message_id;if(!target)throw Error('no_active_turn');
      await api(task?`/api/v1/tasks/${encodeURIComponent(task)}/cancel`:'/api/v1/turn/cancel',{request_id:crypto.randomUUID(),user_message_id:target});notice=t('mobile.shell.stopping');
    }
    if(action==='notifications'){
      if(!pushStatus.push_available)throw Error(t('mobile.shell.host-push-is-not-configured'));
      if(!('Notification' in window)||!('PushManager' in window))throw Error(t('mobile.shell.use-a-supported-browser-on-iphone-add-pot-to-the-home-screen'));
      const permission=await Notification.requestPermission();if(permission!=='granted')throw Error(t('mobile.shell.allow-notifications-in-browser-settings'));
      const reg=await installer.registration();
      const sub=await reg.pushManager.getSubscription()||await reg.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:decode(pushStatus.push_key)});
      await api('/access/push',{subscription:sub.toJSON(),locale:language,private_preview:value==='private'});pushStatus.subscription={enabled:true};notice=t('mobile.shell.main-agent-message-notifications-enabled');
      await api('/access/push/heartbeat',{visible:document.visibilityState==='visible'&&page==='chat'});
    }
    if(action==='notifications-off'){
      await api('/access/push/remove',{});pushStatus.subscription=null;const reg=await navigator.serviceWorker.getRegistration();await (await reg?.pushManager.getSubscription())?.unsubscribe();notice=t('mobile.shell.message-notifications-disabled');
    }
  }catch(e){notice=e.message;}finally{if(write)sending=false;await refresh();}
};
addEventListener('popstate',e=>{page=e.state?.page||'chat';task=e.state?.task||'';scope=e.state?.scope||'active';query=e.state?.query||'';offset=e.state?.offset||0;refresh();});
addEventListener('visibilitychange',()=>{refresh();if(module&&pushStatus.push_available)api('/access/push/heartbeat',{visible:document.visibilityState==='visible'&&page==='chat'}).catch(()=>{});});
async function attempt(fn){try{el('notice').textContent='';await fn();}catch(e){if(el('notice'))el('notice').textContent=e.message;else{notice=e.message;await refresh();}}}
el('login').onclick=()=>attempt(async()=>{const o=options(await api('/access/login/start',{}));const c=await navigator.credentials.get(o);await api('/access/login/finish',credential(c));await start();});
let poll;
el('enroll').onclick=()=>attempt(async()=>{
  clearInterval(poll);const request=await api('/access/enroll',{name:t('mobile.shell.phone')});el('notice').textContent=t('mobile.shell.compare-and-approve-this-code-on-the-host')+request.code;
  poll=setInterval(async()=>{try{const s=await api('/access/enroll/status');if(s.approved){clearInterval(poll);el('register').hidden=false;}}catch(e){clearInterval(poll);el('notice').textContent=e.message;}},1500);
});
el('register').onclick=()=>attempt(async()=>{const o=options(await api('/access/register/start',{}));const c=await navigator.credentials.create(o);await api('/access/register/finish',credential(c));await start();});
el('login').textContent=t('mobile.shell.sign-in-with-passkey');el('enroll').textContent=t('mobile.shell.request-access');el('register').textContent=t('mobile.shell.create-passkey');el('scope').textContent=t('mobile.shell.owner-access-to-this-instance-and-all-its-environments');
if(!window.PublicKeyCredential){el('login').disabled=true;el('enroll').disabled=true;el('notice').textContent=t('mobile.shell.open-this-address-in-safari-or-chrome-with-passkey-support');}else start().catch(e=>el('notice').textContent=e.message);
