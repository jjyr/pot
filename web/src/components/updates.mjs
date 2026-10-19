import {translate} from '../locales/messages.mjs';
import {icon} from './icons.mjs';

// One installation-scoped action shared by the rail and narrow navigation.
// Only the initiating view advances download -> confirmation -> installation.
export function createUpdates({client,locale,changed,notify,reload=()=>location.reload()}) {
  const t=key=>translate(locale(),key),format=(key,values)=>Object.entries(values).reduce((s,[k,v])=>s.replaceAll(`{${k}}`,v),t(key));
  let status=null,scope=null,generation=0,closed=false,pending=false,attempt=null,read=null,reconnect=null,reloading=false;
  const dialog=document.createElement('dialog');dialog.className='preferences update-confirmation';
  const title=document.createElement('h2'),body=document.createElement('p'),actions=document.createElement('div');actions.className='update-actions';
  const cancel=document.createElement('button'),confirm=document.createElement('button');cancel.type=confirm.type='button';cancel.className='control';confirm.className='control primary';actions.append(cancel,confirm);dialog.append(title,body,actions);
  function labels(){
    title.textContent=t('updates.interruptTitle');dialog.setAttribute('aria-label',title.textContent);
    body.textContent=format('updates.interruptBody',{count:Object.values(status?.work||{}).reduce((sum,value)=>sum+value,0)});
    cancel.textContent=t('updates.cancel');confirm.textContent=t('updates.interrupt');confirm.disabled=pending;
  }
  function closeDialog(){if(dialog.open)dialog.close();}
  cancel.onclick=()=>{attempt=null;closeDialog();};dialog.addEventListener('cancel',()=>{attempt=null;});
  confirm.onclick=()=>{closeDialog();install(true);};
  const active=()=>Object.values(status?.work||{}).some(value=>value>0);
  function publish(){if(closed)return;labels();changed();}
  function failed(){notify(t('updates.failed'));attempt=null;pending=false;publish();}
  function recovery(){
    if(reconnect||closed)return;
    reconnect=setTimeout(async()=>{reconnect=null;if(closed)return;try{await refresh();}catch{}if(attempt&&!reloading)recovery();},1500);
  }
  async function refresh(nextScope=scope){
    if(!nextScope||closed)return;
    const captured={owner:'',space:nextScope.space||''},key=JSON.stringify(captured);
    if(JSON.stringify(scope)!==key){scope=captured;generation++;status=null;attempt=null;pending=false;closeDialog();}
    if(read?.generation===generation)return read.promise;
    const own=generation;
    const promise=(async()=>{
      try{
        const value=await client.request('/api/v1/updates',captured);
        if(closed||own!==generation)return;const previous=status?.current;status=value;pending=false;publish();
        if(previous&&previous!==status.current&&status.phase==='current'&&!reloading){attempt=null;reloading=true;reload();return;}
        if(attempt&&status.version===attempt.version){
          if(status.phase==='ready'&&['download','install'].includes(attempt.phase)){
            attempt.phase='confirm';if(active()){labels();dialog.showModal();cancel.focus();}else queueMicrotask(()=>install(false));
          }else if(status.phase==='failed'||status.phase==='blocked'){failed();}
          else if(status.phase==='current'&&status.current===attempt.version){attempt=null;reloading=true;reload();}
        }
      }catch(error){if(closed||own!==generation)return;if(attempt&&['install','confirm'].includes(attempt.phase)){recovery();return;}if(error?.status!==404&&attempt)failed();}
    })();read={generation:own,promise};try{await promise;}finally{if(read?.promise===promise)read=null;}
  }
  async function install(interrupt){
    if(!attempt||pending||closed)return;const own=generation,captured={...scope},version=attempt.version;pending=true;attempt.phase='install';publish();
    try{
      const value=await client.request('/api/v1/updates',{...captured,method:'POST',body:{action:'install',version,request_id:crypto.randomUUID(),interrupt}});
      if(closed||own!==generation)return;status=value;pending=false;publish();recovery();
    }catch(error){if(closed||own!==generation)return;pending=false;if(error.code==='update_busy'){recovery();}else if(error.code==='update_confirmation_required'){attempt.phase='confirm';await refresh();labels();if(!dialog.open)dialog.showModal();}else if(!error.status){recovery();}else failed();}
  }
  async function start(){
    if(pending||attempt||!status?.supported)return;
    attempt={version:status.version,phase:'download'};
    if(status.phase==='ready'){attempt.phase='confirm';if(active()){labels();dialog.showModal();cancel.focus();}else await install(false);return;}
    const own=generation,captured={...scope},version=status.version;pending=true;publish();
    try{const value=await client.request('/api/v1/updates',{...captured,method:'POST',body:{action:'download',version,request_id:crypto.randomUUID(),interrupt:false}});if(closed||own!==generation)return;status=value;pending=false;publish();await refresh();}
    catch{if(!closed&&own===generation)failed();}
  }
  function button(inMenu=false){
    if(!status?.supported||!status.version||status.version===status.current&&status.phase==='current')return null;
    const busy=pending||['downloading','installing','restarting'].includes(status.phase);
    const key=status.phase==='downloading'?'updates.downloading':busy?'updates.installing':'updates.update';
    const button=document.createElement('button');button.type='button';button.className=`update-button ${inMenu?'menu-destination':'rail-button rail-bottom'}`;button.dataset.viewKey='pot-update';
    button.title=busy?t(key):format('updates.tooltip',{version:status.version});button.setAttribute('aria-label',button.title);button.disabled=busy||status.phase==='blocked';button.setAttribute('aria-busy',String(busy));
    button.append(icon(busy?'clock':'update'));if(inMenu){const text=document.createElement('span');text.textContent=t(key);button.append(text);}button.onclick=start;return button;
  }
  const language=()=>publish();window.addEventListener('pot:locale',language);
  return {dialog,button,refresh,close(){closed=true;generation++;clearTimeout(reconnect);closeDialog();dialog.remove();window.removeEventListener('pot:locale',language);}};
}
