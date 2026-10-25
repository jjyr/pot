import {translate} from '../locales/messages.mjs';
import {icon} from './icons.mjs';

// One installation-scoped action shared by the rail and narrow navigation.
// Only the initiating view advances download -> confirmation -> installation.
export function createUpdates({client,locale,changed,notify,reload=()=>location.reload()}) {
  const t=key=>translate(locale(),key),format=(key,values)=>Object.entries(values).reduce((s,[k,v])=>s.replaceAll(`{${k}}`,v),t(key));
  let status=null,scope=null,generation=0,closed=false,pending=false,attempt=null,read=null,reconnect=null,reloading=false,details=false,checking=null,feedback="";
  const listeners=new Set();
  const dialog=document.createElement('dialog');dialog.className='preferences update-confirmation';
  const title=document.createElement('h2'),body=document.createElement('p'),actions=document.createElement('div');actions.className='update-actions';
  const release=document.createElement('a');release.className='update-release';release.target='_blank';release.rel='noopener noreferrer';
  const cancel=document.createElement('button'),confirm=document.createElement('button');cancel.type=confirm.type='button';cancel.className='control';confirm.className='control primary';actions.append(cancel,confirm);dialog.append(title,body,release,actions);
  function labels(){
    title.textContent=details?format('updates.newVersion',{version:status?.version||''}):t('updates.interruptTitle');dialog.setAttribute('aria-label',title.textContent);
    const reason=status?.phase==='blocked'?'recovery':status?.reason||(status?.phase==='failed'?'failed':'');
    body.textContent=details?(reason?t(`updates.reason.${reason}`):t('updates.target')):format('updates.interruptBody',{count:Object.values(status?.work||{}).reduce((sum,value)=>sum+value,0)});
    release.textContent=t('updates.release');release.href=releaseURL();release.hidden=!release.href;
    cancel.textContent=t(details?'updates.close':'updates.cancel');confirm.textContent=t(details?(status?.can_retry?'updates.retry':'updates.update'):'updates.interrupt');confirm.disabled=pending;
    confirm.hidden=details&&!status?.can_retry&&(!status?.auto_install||['blocked','installing','restarting','downloading'].includes(status?.phase));

  }
  function closeDialog(){if(dialog.open)dialog.close();}
  cancel.onclick=()=>{attempt=null;closeDialog();};dialog.addEventListener('cancel',()=>{attempt=null;});
  confirm.onclick=()=>{closeDialog();if(details){details=false;if(status?.can_retry)retry();else start();}else install(true);};
  function releaseURL(){const version=status?.version;return /^\d+\.\d+\.\d+$/.test(version||'')?`https://github.com/jjyr/pot/releases/tag/v${version}`:'';}
  function showDetails(){details=true;labels();if(!dialog.open)dialog.showModal();cancel.focus();}
  const active=()=>Object.values(status?.work||{}).some(value=>value>0);
  function publish(){if(closed)return;labels();for(const entry of listeners){if(JSON.stringify(entry.scope)===JSON.stringify(scope))entry.listener(snapshot());}changed();}
  function snapshot(){return {data:status,checking:!!checking,feedback};}
  function watch(query,listener){const entry={scope:{owner:"",space:query.space||""},listener};listeners.add(entry);if(JSON.stringify(entry.scope)===JSON.stringify(scope))listener(snapshot());return()=>listeners.delete(entry);}
  function failed(){notify(t('updates.failed'));attempt=null;pending=false;publish();}
  function recovery(){
    if(reconnect||closed)return;
    reconnect=setTimeout(async()=>{reconnect=null;if(closed)return;try{await refresh();}catch{}if(attempt&&!reloading)recovery();},1500);
  }
  async function refresh(nextScope=scope){
    if(!nextScope||closed)return;
    const captured={owner:'',space:nextScope.space||''},key=JSON.stringify(captured);
    if(JSON.stringify(scope)!==key){scope=captured;generation++;status=null;attempt=null;pending=false;details=false;checking=null;feedback="";closeDialog();publish();}
    if(read?.generation===generation)return read.promise;
    const own=generation;
    const promise=(async()=>{
      try{
        const value=await client.request('/api/v1/updates',captured);
        if(closed||own!==generation)return;const previous=status?.current;status=value;pending=false;publish();
        if(previous&&previous!==status.current&&status.phase==='current'&&!reloading){attempt=null;reloading=true;reload();return;}
        if(attempt&&status.version===attempt.version){
          if(status.phase==='ready'&&['download','install'].includes(attempt.phase)){
            attempt.phase='confirm';if(active()){details=false;labels();dialog.showModal();cancel.focus();}else queueMicrotask(()=>install(false));
          }else if(status.phase==='failed'||status.phase==='blocked'){failed();}
          else if(status.phase==='current'&&status.current===attempt.version){attempt=null;reloading=true;reload();}
        }
      }catch(error){if(closed||own!==generation)return;if(attempt&&['install','confirm'].includes(attempt.phase)){recovery();return;}if(error?.status!==404&&attempt)failed();}
    })();read={generation:own,promise};try{await promise;}finally{if(read?.promise===promise)read=null;}
  }
  async function check(nextScope=scope){
    if(closed||!nextScope)return;
    const requestedKey=JSON.stringify({owner:'',space:nextScope.space||''});
    if(requestedKey!==JSON.stringify(scope))await refresh(nextScope);
    if(closed||requestedKey!==JSON.stringify(scope))return;
    if(checking)return checking;
    const own=generation,captured={...scope};feedback='checking';
    const promise=(async()=>{
      try{
        const value=await client.request('/api/v1/updates',{...captured,method:'POST',body:{action:'check',request_id:crypto.randomUUID(),version:'',interrupt:false}});
        if(closed||own!==generation)return;
        status=value;feedback=['unreachable','invalid_signature'].includes(value.discovery)?'checkFailed':value.version&&value.version!==value.current?'found':'current';
      }catch{if(!closed&&own===generation)feedback='checkFailed';}
      finally{if(!closed&&own===generation){checking=null;publish();}}
    })();checking=promise;publish();return promise;
  }
  async function install(interrupt){
    if(!attempt||pending||closed)return;const own=generation,captured={...scope},version=attempt.version;pending=true;attempt.phase='install';publish();
    try{
      const value=await client.request('/api/v1/updates',{...captured,method:'POST',body:{action:'install',version,request_id:crypto.randomUUID(),interrupt}});
      if(closed||own!==generation)return;status=value;pending=false;publish();recovery();
    }catch(error){if(closed||own!==generation)return;pending=false;if(error.code==='update_busy'){recovery();}else if(error.code==='update_confirmation_required'){attempt.phase='confirm';await refresh();labels();if(!dialog.open)dialog.showModal();}else if(!error.status){recovery();}else failed();}
  }
  async function retry(){
    if(pending||!status?.can_retry)return;
    const own=generation,captured={...scope};pending=true;publish();
    try{const value=await client.request('/api/v1/updates',{...captured,method:'POST',body:{action:'retry',version:status.version,request_id:crypto.randomUUID(),interrupt:false}});if(closed||own!==generation)return;status=value;pending=false;publish();}
    catch{if(!closed&&own===generation)failed();}
  }
  async function start(){
    if(pending||attempt||!status?.auto_install)return;
    attempt={version:status.version,phase:'download'};
    if(status.phase==='ready'){attempt.phase='confirm';if(active()){details=false;labels();dialog.showModal();cancel.focus();}else await install(false);return;}
    const own=generation,captured={...scope},version=status.version;pending=true;publish();
    try{const value=await client.request('/api/v1/updates',{...captured,method:'POST',body:{action:'download',version,request_id:crypto.randomUUID(),interrupt:false}});if(closed||own!==generation)return;status=value;pending=false;publish();await refresh();}
    catch{if(!closed&&own===generation)failed();}
  }
  function button(inMenu=false){
    if(!status?.version||status.version===status.current&&status.phase!=='blocked')return null;
    const busy=pending||['downloading','installing','restarting'].includes(status.phase);
    const button=document.createElement('button');button.type='button';button.className=`update-button ${inMenu?'menu-destination':'rail-button rail-bottom'}`;button.dataset.viewKey='pot-update';
    button.title=format('updates.newVersion',{version:status.version});button.setAttribute('aria-label',button.title);button.disabled=false;button.setAttribute('aria-busy',String(busy));
    button.append(icon(busy?'clock':'update'));if(inMenu){const text=document.createElement('span');text.textContent=format('updates.newVersion',{version:status.version});button.append(text);}button.onclick=showDetails;return button;
  }
  const language=()=>publish();window.addEventListener('pot:locale',language);
  return {dialog,button,refresh,check,watch,close(){closed=true;generation++;listeners.clear();clearTimeout(reconnect);closeDialog();dialog.remove();window.removeEventListener('pot:locale',language);}};
}
