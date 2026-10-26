import {mountApp} from './components/ui.js';
import {createBrowserClient} from './adapters/browser.js';
import {ensureBrowserAccess, installPWA} from './adapters/access.mjs';
import {APIError, createProductionClient} from './adapters/production.mjs';
import {createStartupIdentity} from './state/startup-identity.mjs';
import {storedPreference} from './state/preferences.mjs';
import {translate} from './locales/messages.mjs';

const root = document.querySelector('#app');
const params = new URLSearchParams(location.search);
const desktop = params.get('platform') === 'desktop';
const changingAccess=new URLSearchParams(location.hash.slice(1)).has('access_token');
const locale=()=>params.get('locale')||storedPreference('pot-ui-locale',navigator.language.startsWith('zh')?'zh-CN':'en');
const t=key=>translate(locale(),key);
document.documentElement.dataset.platform = desktop ? 'desktop' : 'browser';
let dispose=()=>{},client,identity,store,verifying=false,retryTimer,stopped=false,currentAbort,verification=0;
const notice=document.createElement('aside');notice.className='startup-status';notice.setAttribute('role','status');
function status(key){
  notice.replaceChildren(document.createTextNode(t(key)+' '));
  const retry=document.createElement('button');retry.className='text-button';retry.textContent=t('retry');retry.onclick=()=>desktop?location.reload():verify(true);notice.append(retry);
  if(!notice.isConnected)document.body.append(notice);
}
function mount(value){
  dispose();client?.resources?.clear();client=value;
  document.title=client.production?'Pot':'Pot — Go developer preview';
  window.potErrors?.configureIssueReport({version:client.runtime?.version,desktop,openExternal:client.openExternal});
  dispose=mountApp(root,client,{surface:params.get('surface')==='tray'?'tray':'main'});
}
function bindIdentity(value){
  const listeners=new Set();
  value.onStartupConfirmed=handler=>{listeners.add(handler);return()=>listeners.delete(handler);};
  value.revalidateStartup=({resetScope=false}={})=>verify(true,resetScope);
  value.startupConfirmed=()=>{for(const listener of listeners)listener();};
  value.rememberStartup=async(scope,surface)=>{
    // A retained identity may update its local selection, never its confirmed
    // Control/boot locator. No persistence failure may grant network authority.
    if(value!==client||stopped)return;
    const saved=await store.save(value.runtime,scope,surface);if(value===client&&!stopped)identity=saved;
  };
}
const online=()=>{if(!client||client.onlineReady===false)verify();};
function suspend(){if(client?.production){client.onlineReady=false;client.startupConfirmed();status('startup.local');}}
async function verify(force=false,resetScope=false){
  if(stopped||verifying&&force!==true)return;currentAbort?.abort();suspend();verifying=true;clearTimeout(retryTimer);
  const ticket=++verification,abort=new AbortController(),timer=setTimeout(()=>abort.abort(),10000);currentAbort=abort;
  try{
    if(!client&&!changingAccess){
      try{identity=await store.read();if(identity){const value=await createProductionClient({startupIdentity:identity});bindIdentity(value);mount(value);status('startup.local');}}catch(error){console.error('Pot startup storage:',error);}
    }
    const access=await ensureBrowserAccess(root,{signal:abort.signal,onSetup:()=>{if(ticket!==verification||stopped)return;dispose();client?.resources?.clear();client=null;identity=null;store?.clear().catch(()=>{});notice.remove();}});
    const response=await fetch('/api/v1/runtime',{credentials:'same-origin',signal:abort.signal});
    const runtime=await response.json().catch(()=>null);
    if(ticket!==verification||stopped)return;
    if(!response.ok){
      if(response.status===404&&!identity){const preview=createBrowserClient(),snapshot=await preview.snapshot();if(snapshot.runtime_id==='pot-ui-preview'){mount(preview);notice.remove();return;}}
      throw new APIError(response.status,runtime);
    }
    if(!(runtime?.runtime_id||runtime?.id))throw Error('Runtime identity unavailable');
    const id=runtime.runtime_id||runtime.id;
    if(client&&client.runtime.runtime_id===id&&!resetScope){
      client.access=access;client.confirmStartup(runtime);client.startupConfirmed();
    }else{
      // A changed Control retires every old writer before opening its namespace.
      // Old drafts remain under their original full identity and are not copied.
      dispose();client?.resources?.clear();client=null;
      identity=await store.save(runtime).catch(()=>null);
      const value=await createProductionClient({startupIdentity:{runtime_id:id,boot_id:runtime.boot_id||''}});
      value.confirmStartup(runtime);value.access=access;bindIdentity(value);mount(value);
    }
    notice.remove();
  }catch(error){
    if(ticket!==verification||stopped)return;
    console.error('Pot startup:',error);
    status(error.status===401?'connection.authenticationRequired':error.status===403?'startup.forbidden':identity?'startup.local':'startup.unavailable');
    // Retry only authentication/identity reads. No command or interrupted send
    // is queued here. Explicit access failures retain their existing flow.
    if(![401,403].includes(error.status))retryTimer=setTimeout(()=>verify(),3000);
  }finally{clearTimeout(timer);if(ticket===verification)verifying=false;}
}
try{
  if(desktop){
    mount(params.get('mode')==='preview'?(await import('./adapters/desktop.js')).createDesktopClient():await createProductionClient({desktop}));
  }else{
    installPWA().catch(error=>console.error('Pot shell installation:',error));
    store=createStartupIdentity();status('startup.loading');
    try{identity=changingAccess?null:await store.read();}catch(error){console.error('Pot startup storage:',error);}
    if(identity){const value=await createProductionClient({startupIdentity:identity});bindIdentity(value);mount(value);status('startup.local');}
    else status('startup.loading');
    verify();
    window.addEventListener('offline',suspend);
    window.addEventListener('online',online);
  }
}catch(error){console.error('Pot startup:',error);status('startup.unavailable');}
window.addEventListener('pagehide',event=>{if(!event.persisted){stopped=true;clearTimeout(retryTimer);dispose();client?.resources?.clear();store?.close();window.removeEventListener('online',online);window.removeEventListener('offline',suspend);}});
