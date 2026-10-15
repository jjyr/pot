let composing=false;
addEventListener("compositionstart",()=>{composing=true;},true);
addEventListener("compositionend",()=>{composing=false;},true);
window.potIsComposing=()=>composing;

let machineReply=null,machinePending=false;
import {createInstaller, isInstalled} from '/install.js';
import {subscribe} from '/events.js';
let module, page='chat', task='', snapshot, notice='', sending=false, refreshing=false, accepted;
let execution_target='', scope='active', query='', offset=0, nextOffset=null, usageDays=7, usageOffset=0, pushStatus;
let assistantId=initialAssistant(), pendingOwner='main', assistantSavedOwner=null, assistantSaveSequence=0, assistantSaving=false;
function initialAssistant(){return new URLSearchParams(location.search).get('assistant')||'main';}
let pageData={}, connectionNotice=false, accessToken='', authenticated=false;
const incomingToken=new URLSearchParams(location.hash.slice(1)).get('access_token');
if(incomingToken!==null)history.replaceState(history.state,'',location.pathname+location.search);
const initialRoute=new URLSearchParams(location.search);
task=initialRoute.get('task')||'';page=task?'task':(['chat','tasks','machines','more','usage','schedules'].includes(initialRoute.get('page'))?initialRoute.get('page'):'chat');
function navigate(){history.pushState({page,task,scope,query,offset,assistantId,execution_target},'',(task?'/?task='+encodeURIComponent(task):'/?page='+page)+'&assistant='+encodeURIComponent(assistantId));}

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
  stopSubscriptions();invalidate(['*']);authenticated=false;el('auth').hidden=false;el('notice').textContent=message;
  if(module)module.update_view(JSON.stringify({...pageData,connected:false,notice:message}));
}
function scopedPath(path,owner=assistantId){
  if(path.startsWith('/api/v1/')&&!path.startsWith('/api/v1/assistants')&&!path.startsWith('/api/v1/assistant-tasks')&&path!=='/api/v1/runtime'&&path!=='/api/v1/machines'&&path!=='/api/v1/machine-connections'&&path!=='/api/v1/changes')path='/api/v1/assistants/'+encodeURIComponent(owner)+'/'+path.slice(8);
  return path;
}
async function api(path,value,owner=assistantId){
  path=scopedPath(path,owner);

  const r=await fetch(path,{credentials:'same-origin',cache:'no-store',headers:{...(value===undefined?{}:{'Content-Type':'application/json'}),...(accessToken?{Authorization:'Bearer '+accessToken}:{})},...(value===undefined?{}:{method:'POST',body:JSON.stringify(value)})});
  if(r.status===401){configureNotice(t('web.token-invalid'));throw Error(t('web.token-invalid'));}
  const body=await r.json();if(!r.ok)throw Error(body.error||body.message||body.code||String(r.status));if(value!==undefined&&path.startsWith('/api/'))invalidate([domain(path)]);return body;
}
async function configure(token){
  accessToken=token.trim();if(!/^[A-Za-z0-9_-]{43}$/.test(accessToken))throw Error(t('web.token-invalid'));
  await api('/access/token',{});await tokenStore(accessToken);el('access-token').value='';await start();
}
const projections=new Map();let retryRefresh,refreshDelay=250;let cacheRevision=0,refreshAgain=false,stopChanges,stopMachineWatch,stopConversation,conversationKey='',versions=null,streamGeneration=0;
function domain(path){
  if(path.includes('/snapshot')||/\/tasks\/[^/?]+$/.test(path))return 'conversation';
  if(path.includes('/assistant-tasks')||path.includes('/tasks/')||path.endsWith('/tasks'))return 'tasks';
  if(path.includes('/permissions'))return 'permissions';
  if(path.includes('/schedules'))return 'schedules';
  if(path.includes('/usage'))return 'usage';
  if(path.includes('/agent')||path.includes('/instructions')||path.includes('/providers')||path.includes('/completions'))return 'settings';
  if(path.endsWith('/assistants'))return 'assistants';return 'machines';
}
function invalidate(topics){
  cacheRevision++;
  for(const key of projections.keys())if(topics.includes('*')||topics.some(t=>t.split('/')[0]===domain(key)))projections.delete(key);
}
async function projection(path,unused,owner=assistantId){
  const key=scopedPath(path,owner);if(projections.has(key))return projections.get(key);
  const revision=cacheRevision,value=await api(path,undefined,owner);
  if(projections.has(key))return projections.get(key);
  if(revision===cacheRevision){projections.set(key,value);if(projections.size>128)projections.delete(projections.keys().next().value);}return value;
}
function stopSubscriptions(){clearTimeout(retryRefresh);retryRefresh=undefined;stopChanges?.();stopConversation?.();stopMachineWatch?.();stopChanges=stopConversation=stopMachineWatch=undefined;conversationKey='';versions=null;streamGeneration++;}
function ensureSubscriptions(){
  if(!authenticated||!module||document.visibilityState==='hidden'){stopSubscriptions();return;}
  const headers=accessToken?{Authorization:'Bearer '+accessToken}:{};
  if(page==='machines'){
    if(!stopMachineWatch)stopMachineWatch=subscribe('/api/v1/machines/watch',headers,()=>{},()=>{});
  }else{stopMachineWatch?.();stopMachineWatch=undefined;}
  if(!stopChanges)stopChanges=subscribe('/api/v1/changes',headers,frame=>{
    const next=JSON.parse(frame.data),initial=!versions||versions.boot_id!==next.boot_id||versions.runtime_id!==next.runtime_id;
    const dirty=initial?['*']:Object.keys(next.versions).filter(k=>next.versions[k]!==versions.versions[k]);versions=next;
    if(initial){stopConversation?.();stopConversation=undefined;conversationKey='';streamGeneration++;}
    if(dirty.length){invalidate(dirty.filter(t=>t.split('/')[0]!=='conversation'||!stopConversation));refresh();}
  },error=>{versions=null;connectionNotice=true;notice=t('mobile.shell.connection-unavailable')+error.message;
    module.update_view(JSON.stringify({...pageData,connected:false,notice}));if(error.status===401||error.status===403)configureNotice(t('web.token-invalid'));});
  const key=(page==='chat'||page==='task')?assistantId+'/'+task:'';
  if(conversationKey!==key){stopConversation?.();stopConversation=undefined;conversationKey=key;streamGeneration++;}
  if(key&&!stopConversation&&snapshot&&pageData.conversation===(task||assistantId)){
    const owner=assistantId,selected=task,generation=streamGeneration;
    const path=scopedPath(selected?'/api/v1/tasks/'+encodeURIComponent(selected)+'/events':'/api/v1/events?snapshot=true',owner);
    stopConversation=subscribe(path,headers,frame=>{
      if(generation!==streamGeneration||owner!==assistantId||selected!==task)return;
      const next=frame.event==='snapshot'?JSON.parse(frame.data):JSON.parse(module.apply_conversation_event(JSON.stringify(snapshot),frame.data));
      if(next.conversation_id!==(selected||owner))throw Error('Conversation mismatch');
      snapshot=next;const cacheKey=scopedPath(selected?'/api/v1/tasks/'+encodeURIComponent(selected):'/api/v1/snapshot',owner);projections.set(cacheKey,next);
      const receipt=pendingOwner===owner?JSON.parse(module.reconcile_send(JSON.stringify(next))):null;if(receipt)accepted=receipt;
      connectionNotice=false;pageData={...pageData,snapshot:next,accepted,connected:true,notice:''};module.update_view(JSON.stringify(pageData));
    },error=>{if(generation!==streamGeneration)return;connectionNotice=true;notice=t('mobile.shell.connection-unavailable')+error.message;
      module.update_view(JSON.stringify({...pageData,connected:false,notice}));if(error.status===401||error.status===403)configureNotice(t('web.token-invalid'));});
  }
}
async function refresh(){
  ensureSubscriptions();
  if(!module||!authenticated||document.visibilityState==='hidden')return;if(refreshing){refreshAgain=true;return;}refreshing=true;clearTimeout(retryRefresh);
  const requestedGeneration=streamGeneration,requestedRevision=cacheRevision;
  const requestedAssistant=assistantId, route=task, requestedPage=page, requestedQuery=`${scope}:${query}:${execution_target}:${offset}:${usageDays}:${usageOffset}`;
  try{
    const directory=await projection('/api/v1/runtime');
    const required={chat:'conversation',task:'tasks',tasks:'tasks',machines:'runtime',usage:'accounting',schedules:'schedules'}[requestedPage];
    if(required&&!directory.capabilities?.includes(required))throw Error('Runtime capability unavailable: '+required);
    if(requestedPage==='machines'){
      const machines=await projection('/api/v1/machines');
      if(requestedGeneration!==streamGeneration||requestedRevision!==cacheRevision||requestedAssistant!==assistantId||requestedPage!==page)return;
      const local=machines.machines.find(m=>m.local);
      const data={connected:true,page,locale:language,runtime:directory,machines,machine_reply:machineReply,machine_pending:machinePending,
        sandbox_inventory:{items:local?.execution_targets.filter(e=>e.execution_target?.target.kind==='sandbox')||[],selected:local?.default},
        dark:matchMedia('(prefers-color-scheme: dark)').matches,reduce_motion:matchMedia('(prefers-reduced-motion: reduce)').matches};
      refreshDelay=250;pageData=data;module.update_view(JSON.stringify(data));return;
    }
    const current=await projection(route?`/api/v1/tasks/${encodeURIComponent(route)}`:'/api/v1/snapshot',undefined,requestedAssistant);
    if(requestedGeneration!==streamGeneration||requestedRevision!==cacheRevision||requestedAssistant!==assistantId||route!==task||requestedPage!==page)return;
    snapshot=current;
    const receipt=pendingOwner===requestedAssistant?JSON.parse(module.reconcile_send(JSON.stringify(current))):null;
    if(receipt){accepted=receipt;notice=t('mobile.shell.received');}
    const data={connected:true,reduce_motion:matchMedia('(prefers-reduced-motion: reduce)').matches,page,conversation:task||assistantId,locale:language,notice,snapshot,accepted,installed:isInstalled(),dark:matchMedia('(prefers-color-scheme: dark)').matches};
    data.runtime=directory;data.assistant_id=requestedAssistant;data.assistants=await projection('/api/v1/assistants');data.assistant_saved_owner=assistantSavedOwner;data.assistant_save_sequence=assistantSaveSequence;
    if(page==='chat'){data.completions=await projection('/api/v1/completions',undefined,requestedAssistant);data.assistant_rules=await projection(`/api/v1/assistants/${encodeURIComponent(requestedAssistant)}/instructions`);data.assistant_configuration=await projection('/api/v1/agent',undefined,requestedAssistant);}
    if(page==='chat'||page==='task')data.permissions=await projection('/api/v1/permissions',undefined,requestedAssistant);
    if(page==='tasks'){const result=await projection(`/api/v1/assistant-tasks?scope=${scope}&q=${encodeURIComponent(query)}&execution_target=${encodeURIComponent(execution_target)}&offset=${offset}&limit=20`);data.task_page=result;data.task_query={scope,q:query,execution_target,offset,limit:20,selected:''};nextOffset=result.next_offset;}
    if(page==='usage'){data.usage=await projection('/api/v1/usage/summary?days='+usageDays+'&offset='+usageOffset+'&timezone='+encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone),undefined,requestedAssistant);}
    if(page==='schedules'){data.schedules=await projection('/api/v1/schedules',undefined,requestedAssistant);}
    if(requestedGeneration!==streamGeneration||requestedRevision!==cacheRevision||requestedAssistant!==assistantId||route!==task||requestedPage!==page||requestedQuery!==`${scope}:${query}:${execution_target}:${offset}:${usageDays}:${usageOffset}`)return;
    if(connectionNotice){notice='';connectionNotice=false;data.notice='';}
    if(snapshot?.conversation_id===(route||requestedAssistant))data.snapshot=snapshot;
    refreshDelay=250;pageData=data;module.update_view(JSON.stringify(data));
  }catch(e){if(requestedGeneration!==streamGeneration||requestedRevision!==cacheRevision||requestedAssistant!==assistantId||route!==task||requestedPage!==page)return;retryRefresh=setTimeout(refresh,refreshDelay);refreshDelay=Math.min(refreshDelay*2,15000);connectionNotice=true;notice=t('mobile.shell.connection-unavailable')+e.message;module.update_view(JSON.stringify({...pageData,connected:false,page,conversation:task||assistantId,locale:language,notice,page_error:e.message,snapshot,accepted}));}
  finally{refreshing=false;ensureSubscriptions();if(refreshAgain){refreshAgain=false;queueMicrotask(refresh);}}
}
async function start(){
  pushStatus=await api('/access/status');authenticated=pushStatus.authenticated;el('configure-token').disabled=false;if(authenticated)el('auth').hidden=true;
  if(pushStatus.authenticated&&module){await refresh();ensureSubscriptions();}
  if(pushStatus.authenticated&&!module){
    module=await import('/pkg/pot_web_ui.js');await module.default();module.run();
    history.replaceState({page,task,scope,query,offset,assistantId,execution_target},'');
    await refresh();ensureSubscriptions();
    setInterval(()=>{if(authenticated&&pushStatus.push_available)api('/access/push/heartbeat',{visible:document.visibilityState==='visible'&&page==='chat'}).catch(()=>{});},10000);
  }
}
async function sendPending(){
  const sent=JSON.parse(module.pending_send());
  if(!sent)return;
  const path=sent.conversation_id===pendingOwner?'/api/v1/messages':`/api/v1/tasks/${encodeURIComponent(sent.conversation_id)}/messages`;
  const result=await api(path,sent,pendingOwner);
  const receipt=JSON.parse(module.accept_send(sent.request_id,JSON.stringify(result)));
  if(receipt)accepted=receipt;notice=t('mobile.shell.received');
}
window.potAction=async(action,value)=>{
  if(action==='machine-command'){
    if(machinePending)return;
    machinePending=true;module.update_view(JSON.stringify({...pageData,machine_pending:true}));
    const intent=JSON.parse(value);
    try{machineReply={intent,result:await api('/api/v1/machine-connections',intent.command)};}
    catch(e){machineReply={intent,error:e.message};}
    finally{machinePending=false;await refresh();}
    return;
  }
  const write=action==='send'||action==='retry';
  if(write&&sending)return;
  if(action==='assistant-save'&&assistantSaving)return;
  if(action==='assistant-save')assistantSaving=true;
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
    if(action==='assistant-select'){if(sending||JSON.parse(module.pending_send()))throw Error(t('mobile.shell.resolve-the-previous-send-using-retry-first'));assistantId=value;task='';page='chat';snapshot=undefined;pageData={};navigate();}
    if(action==='assistant-save'){
      const draft=JSON.parse(value), owner=draft.owner;
      if(owner==='new')await api('/api/v1/assistants',{request_id:draft.request_id,name:draft.name});
      else {
        await api(`/api/v1/assistants/${encodeURIComponent(owner)}/definition`,draft);
      }
      assistantSavedOwner=owner;assistantSaveSequence++;
    }
    if(action==='assistant-profile')await api('/api/v1/agent/config',{request_id:requestUUID(),owner:assistantId,profile:value});
    if(action==='install')await installer.open();
    if(action==='page'){page=value;task='';navigate();}
    if(action==='task'){if(sending||JSON.parse(module.pending_send()))throw Error(t('mobile.shell.resolve-the-previous-send-using-retry-first'));let entry;try{entry=JSON.parse(value);}catch{}if(entry?.id){task=entry.id;assistantId=entry.assistant_id;}else task=typeof entry==='string'?entry:value;page='task';navigate();}
    if(action==='file-download'){
      const file=JSON.parse(value), owner=assistantId;
      const base=`/api/v1/assistants/${encodeURIComponent(owner)}/tasks/${encodeURIComponent(file.task_id)}`;
      const chunk=await api(`${base}/files?path=${encodeURIComponent(file.path)}`,undefined,owner);
      if(owner!==assistantId)throw Error(t('files.assistant-changed'));
      const link=document.createElement('a');link.href=`${base}/file-download?path=${encodeURIComponent(file.path)}&revision=${encodeURIComponent(chunk.revision)}`;
      link.download=file.path.split('/').pop();document.body.appendChild(link);link.click();link.remove();
    }
    if(action==='filter'){scope=value;offset=0;}
    if(action==='execution-target-tasks'){execution_target=value;page='tasks';task='';scope='active';query='';offset=0;navigate();}
    if(action==='execution-target-filter-clear'){execution_target='';offset=0;}
    if(action==='task-query'){const q=JSON.parse(value);scope=q.scope;query=q.q;offset=q.offset;execution_target=q.execution_target||'';}
    if(action==='sandbox-lifecycle'){const body=JSON.parse(value);body.request_id=requestUUID();delete body.sandbox_id;await api('/api/v1/sandboxes/shared',body);}
    if(action==='permission-decision')await api('/api/v1/permissions',JSON.parse(value));
    if(action==='usage-query'){const q=JSON.parse(value);usageDays=q.days;usageOffset=q.offset;}
    if(action==='search'){query=value;offset=0;}
    if(action==='next'&&nextOffset!=null)offset=nextOffset;
    if(action==='previous')offset=Math.max(0,offset-20);
    if(action==='language'){language=languages[(languages.indexOf(language)+1)%languages.length];try{localStorage.setItem('pot-locale',language);}catch{}if(pushStatus.subscription?.enabled)await api('/access/push/preferences',{locale:language});}
    if(action==='configure-token'){configureNotice();return;}
    if(action==='logout'){await api('/access/logout',{});await tokenStore(undefined,true);const reg=await navigator.serviceWorker?.getRegistration();await (await reg?.pushManager.getSubscription())?.unsubscribe();location.reload();return;}
    if(action==='send'){
      if(JSON.parse(module.pending_send()))throw Error(t('mobile.shell.resolve-the-previous-send-using-retry-first'));
      pendingOwner=assistantId;module.begin_send(task||assistantId,value,requestUUID());
      notice=t('mobile.shell.sending');module.update_view(JSON.stringify({...pageData,notice}));await sendPending();
    }
    if(action==='retry')await sendPending();
    if(action==='stop'){
      const target=snapshot?.turn?.user_message_id;if(!target)throw Error('no_active_turn');
      await api(task?`/api/v1/tasks/${encodeURIComponent(task)}/interrupt`:'/api/v1/turn/cancel',{request_id:requestUUID(),user_message_id:target});notice=t('mobile.shell.stopping');
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
  }catch(e){notice=e.message;}finally{if(write)sending=false;if(action==='assistant-save')assistantSaving=false;await refresh();}
};
addEventListener('popstate',e=>{page=e.state?.page||'chat';task=e.state?.task||'';assistantId=e.state?.assistantId||'main';scope=e.state?.scope||'active';query=e.state?.query||'';offset=e.state?.offset||0;execution_target=e.state?.execution_target||'';refresh();});
addEventListener('visibilitychange',()=>{if(document.visibilityState==='hidden')stopSubscriptions();else invalidate(['*']);refresh();if(module&&pushStatus.push_available)api('/access/push/heartbeat',{visible:document.visibilityState==='visible'&&page==='chat'}).catch(()=>{});});
async function attempt(fn){try{el('notice').textContent='';await fn();}catch(e){if(el('notice'))el('notice').textContent=e.message;else{notice=e.message;await refresh();}}}
el('configure-token').onclick=()=>attempt(async()=>{
  el('configure-token').disabled=true;
  try{await configure(el('access-token').value);await refresh();}finally{el('configure-token').disabled=false;}
});
el('access-token').onkeydown=event=>{if(event.key==='Enter')el('configure-token').click();};
el('configure-token').textContent=t('web.configure-token');el('scope').textContent=t('mobile.shell.owner-access-to-this-instance-and-all-its-sandboxs');
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

for(const query of ['(prefers-color-scheme: dark)','(prefers-reduced-motion: reduce)'])matchMedia(query).addEventListener('change',()=>refresh());
addEventListener('pagehide',stopSubscriptions);
addEventListener('online',()=>{stopSubscriptions();invalidate(['*']);refresh();});
