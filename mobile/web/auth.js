import {createInstaller, isInstalled} from '/install.js';
let module, page='chat', task='', snapshot, notice='', sending=false, refreshing=false, accepted;
let scope='active', query='', offset=0, nextOffset=null, usageDays=7, usageOffset=0, pushStatus;
let pageData={}, connectionNotice=false, accessToken='', authenticated=false;
const incomingToken=new URLSearchParams(location.hash.slice(1)).get('access_token');
if(incomingToken!==null)history.replaceState(history.state,'',location.pathname+location.search);
const initialRoute=new URLSearchParams(location.search);
task=initialRoute.get('task')||'';page=task?'task':(['chat','tasks','sandbox','more','usage','schedules'].includes(initialRoute.get('page'))?initialRoute.get('page'):'chat');
function navigate(){history.pushState({page,task,scope,query,offset},'',task?'/?task='+encodeURIComponent(task):'/?page='+page);}
const el=id=>document.getElementById(id);
function requestUUID(){
  if(crypto.randomUUID)return crypto.randomUUID();
  const bytes=crypto.getRandomValues(new Uint8Array(16));bytes[6]=(bytes[6]&15)|64;bytes[8]=(bytes[8]&63)|128;
  const h=Array.from(bytes,b=>b.toString(16).padStart(2,'0')).join('');
  return `${h.slice(0,8)}-${h.slice(8,12)}-${h.slice(12,16)}-${h.slice(16,20)}-${h.slice(20)}`;
}
const catalogs=await fetch('/locales.json',{cache:'no-store'}).then(r=>r.json());
const languages=Object.keys(catalogs).sort();
function matchLocale(value){const normalized=value.replaceAll('_','-').toLowerCase();return languages.find(l=>l.toLowerCase()===normalized)||languages.find(l=>l.toLowerCase().split('-')[0]===normalized.split('-')[0]);}
function initialLocale(){try{const saved=localStorage.getItem('pot-locale');if(saved&&matchLocale(saved))return matchLocale(saved);}catch{}return (navigator.languages||[]).map(matchLocale).find(Boolean)||'en';}
let language=initialLocale();
const t=key=>catalogs[language]?.[key]||catalogs.en[key]||key;
const installer=createInstaller(t,()=>refresh());
const decode=s=>Uint8Array.from(atob(s.replace(/-/g,'+').replace(/_/g,'/')),c=>c.charCodeAt(0));
async function tokenStore(value, remove=false){
  const db=await new Promise((resolve,reject)=>{const request=indexedDB.open('pot-access',1);request.onupgradeneeded=()=>request.result.createObjectStore('tokens');request.onsuccess=()=>resolve(request.result);request.onerror=()=>reject(request.error);});
  try{return await new Promise((resolve,reject)=>{const tx=db.transaction('tokens',value!==undefined||remove?'readwrite':'readonly');const store=tx.objectStore('tokens');const req=remove?store.delete('current'):value!==undefined?store.put(value,'current'):store.get('current');let result;req.onsuccess=()=>result=req.result;tx.oncomplete=()=>resolve(result);tx.onerror=()=>reject(tx.error);tx.onabort=()=>reject(tx.error);});}finally{db.close();}
}
function configureNotice(message=''){
  authenticated=false;el('auth').hidden=false;el('notice').textContent=message;
  if(module)module.update_view(JSON.stringify({...pageData,connected:false,notice:message}));
}
async function api(path,value){
  const r=await fetch(path,{credentials:'same-origin',cache:'no-store',headers:{...(value===undefined?{}:{'Content-Type':'application/json'}),...(accessToken?{Authorization:'Bearer '+accessToken}:{})},...(value===undefined?{}:{method:'POST',body:JSON.stringify(value)})});
  if(r.status===401){configureNotice(t('web.token-invalid'));throw Error(t('web.token-invalid'));}
  const body=await r.json();if(!r.ok)throw Error(body.code||String(r.status));return body;
}
async function configure(token){
  accessToken=token.trim();if(!/^[A-Za-z0-9_-]{43}$/.test(accessToken))throw Error(t('web.token-invalid'));
  await api('/access/token',{});await tokenStore(accessToken);el('access-token').value='';await start();
}
async function refresh(){
  if(!module||!authenticated||refreshing||document.visibilityState==='hidden')return;refreshing=true;
  const route=task, requestedPage=page, requestedQuery=`${scope}:${query}:${offset}:${usageDays}:${usageOffset}`;
  try{
    const current=await api(route?`/api/v1/tasks/${encodeURIComponent(route)}`:'/api/v1/snapshot');
    if(route!==task||requestedPage!==page)return;
    snapshot=current;
    const receipt=JSON.parse(module.reconcile_send(JSON.stringify(current)));
    if(receipt){accepted=receipt;notice=t('mobile.shell.received');}
    const data={connected:true,reduce_motion:matchMedia('(prefers-reduced-motion: reduce)').matches,page,conversation:task||'main',locale:language,notice,snapshot,accepted,installed:isInstalled(),dark:matchMedia('(prefers-color-scheme: dark)').matches};
    if(page==='tasks'){const result=await api(`/api/v1/tasks/query?scope=${scope}&q=${encodeURIComponent(query)}&offset=${offset}&limit=20`);data.task_page=result;data.task_query={scope,q:query,offset,limit:20,selected:''};nextOffset=result.next_offset;}
    if(page==='sandbox'){const result=await api('/api/v1/sandboxes');data.sandbox_inventory=result;}
    if(page==='usage'){data.usage=await api('/api/v1/usage/summary?days='+usageDays+'&offset='+usageOffset+'&timezone='+encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone));}
    if(page==='schedules'){data.schedules=await api('/api/v1/schedules');}
    if(route!==task||requestedPage!==page||requestedQuery!==`${scope}:${query}:${offset}:${usageDays}:${usageOffset}`)return;
    if(connectionNotice){notice='';connectionNotice=false;data.notice='';}
    pageData=data;module.update_view(JSON.stringify(data));
  }catch(e){connectionNotice=true;notice=t('mobile.shell.connection-unavailable')+e.message;module.update_view(JSON.stringify({...pageData,connected:false,page,conversation:task||'main',locale:language,notice,page_error:e.message,snapshot,accepted}));}
  finally{refreshing=false;}
}
async function start(){
  pushStatus=await api('/access/status');authenticated=pushStatus.authenticated;el('configure-token').disabled=false;if(authenticated)el('auth').hidden=true;
  if(pushStatus.authenticated&&!module){
    module=await import('/pkg/pot_mobile.js');await module.default();module.run();
    history.replaceState({page,task,scope,query,offset},'');
    await refresh();setInterval(refresh,1500);
    setInterval(()=>{if(authenticated&&pushStatus.push_available)api('/access/push/heartbeat',{visible:document.visibilityState==='visible'&&page==='chat'}).catch(()=>{});},10000);
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
    if(action==='desktop'){
      const viewer=window.open('about:blank','_blank');
      if(viewer)viewer.opener=null;
      try {const result=await api('/api/v1/desktop/sessions',{sandbox_id:value});
        if(!result.path)throw Error(result.phase||'desktop_unavailable');
        if(viewer)viewer.location.replace(result.path);else location.assign(result.path);
      }catch(error){viewer?.close();throw error;}
    }
    if(action==='install')await installer.open();
    if(action==='page'){page=value;task='';navigate();}
    if(action==='task'){task=value;page='task';navigate();}
    if(action==='filter'){scope=value;offset=0;}
    if(action==='task-query'){const q=JSON.parse(value);scope=q.scope;query=q.q;offset=q.offset;}
    if(action==='usage-query'){const q=JSON.parse(value);usageDays=q.days;usageOffset=q.offset;}
    if(action==='search'){query=value;offset=0;}
    if(action==='next'&&nextOffset!=null)offset=nextOffset;
    if(action==='previous')offset=Math.max(0,offset-20);
    if(action==='language'){language=languages[(languages.indexOf(language)+1)%languages.length];try{localStorage.setItem('pot-locale',language);}catch{}if(pushStatus.subscription?.enabled)await api('/access/push/preferences',{locale:language});}
    if(action==='configure-token'){configureNotice();return;}
    if(action==='logout'){await api('/access/logout',{});await tokenStore(undefined,true);const reg=await navigator.serviceWorker?.getRegistration();await (await reg?.pushManager.getSubscription())?.unsubscribe();location.reload();return;}
    if(action==='send'){
      if(JSON.parse(module.pending_send()))throw Error(t('mobile.shell.resolve-the-previous-send-using-retry-first'));
      module.begin_send(task||'main',value,requestUUID());
      notice=t('mobile.shell.sending');await sendPending();
    }
    if(action==='retry')await sendPending();
    if(action==='stop'){
      const target=snapshot?.turn?.user_message_id;if(!target)throw Error('no_active_turn');
      await api(task?`/api/v1/tasks/${encodeURIComponent(task)}/cancel`:'/api/v1/turn/cancel',{request_id:requestUUID(),user_message_id:target});notice=t('mobile.shell.stopping');
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
el('configure-token').onclick=()=>attempt(async()=>{
  el('configure-token').disabled=true;
  try{await configure(el('access-token').value);await refresh();}finally{el('configure-token').disabled=false;}
});
el('access-token').onkeydown=event=>{if(event.key==='Enter')el('configure-token').click();};
el('configure-token').textContent=t('web.configure-token');el('scope').textContent=t('mobile.shell.owner-access-to-this-instance-and-all-its-environments');
el('access-token').placeholder=t('web.access-token');el('access-token').setAttribute('aria-label',t('web.access-token'));
try{
  if(incomingToken!==null)await configure(incomingToken);
  else {accessToken=await tokenStore()||'';if(accessToken)await configure(accessToken);}
}catch(e){configureNotice(e.message);}

addEventListener('hashchange',()=>{
  const token=new URLSearchParams(location.hash.slice(1)).get('access_token');
  if(token===null)return;
  history.replaceState(history.state,'',location.pathname+location.search);
  attempt(async()=>{await configure(token);await refresh();});
});
