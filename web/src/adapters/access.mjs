import {translate} from '../locales/messages.mjs';
import {storedPreference} from '../state/preferences.mjs';
import {disableNotificationInstallation} from './pwapush.mjs';
import {clearBusinessCaches} from '../state/cache-lifecycle.mjs';
// The installation token is origin-scoped in IndexedDB. API calls use the
// server's HttpOnly session cookie; credentials never enter routes or logs.
import {bindViewport} from '../components/viewport.mjs';

const database = 'pot-browser-access';
let installationPrompt;
if(typeof window!=='undefined')window.addEventListener('beforeinstallprompt',event=>{event.preventDefault();installationPrompt=event;});
async function tokenStore(mode, value) {
  return new Promise((resolve,reject)=>{
    const open=indexedDB.open(database,1);open.onupgradeneeded=()=>open.result.createObjectStore('installation');
    open.onerror=()=>reject(open.error);open.onsuccess=()=>{
      const db=open.result,transaction=db.transaction('installation',mode==='get'?'readonly':'readwrite'),store=transaction.objectStore('installation');
      const result=mode==='get'?store.get('token'):mode==='remove'?store.delete('token'):store.put(value,'token');
      result.onerror=()=>reject(result.error);transaction.oncomplete=()=>{const saved=result.result;db.close();resolve(saved);};transaction.onerror=()=>{db.close();reject(transaction.error);};
    };
  });
}
export function takeAccessFragment(locationObject=location,historyObject=history) {
  const fragment=new URLSearchParams(locationObject.hash.slice(1));
  const token=fragment.get('access_token');if(token===null)return null;
  fragment.delete('access_token');historyObject.replaceState(historyObject.state,'',`${locationObject.pathname}${locationObject.search}${fragment.size?'#'+fragment:''}`);return token;
}
async function exchange(token,signal) {
  const response=await fetch('/access/token',{method:'POST',credentials:'same-origin',signal,headers:{Authorization:`Bearer ${token}`}});
  if(!response.ok)throw Object.assign(Error('Browser access exchange failed'),{status:response.status});
}
export async function ensureBrowserAccess(root,{signal,onSetup=()=>{}}={}) {
  const fragment=takeAccessFragment();
  const response=await fetch('/access/status',{credentials:'same-origin',signal});
  if(response.status===404)return null; // Isolated fixture is checked by bootstrap.
  if(!response.ok)throw Object.assign(Error(`Browser access: HTTP ${response.status}`),{status:response.status});
  const status=await response.json();signal?.throwIfAborted();
  let saved=fragment;
  if(!saved)try{saved=await tokenStore('get');}catch{/* A restricted browser can use a session-only token. */}
  signal?.throwIfAborted();
  if(fragment||!status.authenticated&&saved) {
    try {await exchange(saved,signal);try{await tokenStore('put',saved);}catch{}return {...status,authenticated:true};}
    catch(reason) {if(![401,403].includes(reason.status))throw reason;try{await tokenStore('remove');}catch{}if(status.authenticated&&!fragment)return status;}
  }
  if(status.authenticated&&!fragment)return status;
  onSetup();
  const t=key=>translate(new URLSearchParams(location.search).get('locale')||storedPreference('pot-ui-locale',navigator.language.startsWith('zh')?'zh-CN':'en'),key);
  const stopViewport=bindViewport(document.documentElement);
  return new Promise(resolve=>{
    const form=document.createElement('form'),heading=document.createElement('h1'),label=document.createElement('label'),input=document.createElement('input'),submit=document.createElement('button'),error=document.createElement('p');
    root.className='access-setup';heading.textContent=t('startup.connectTitle');label.textContent=t('startup.token');input.type='password';input.autocomplete='off';input.required=true;input.setAttribute('aria-label',label.textContent);input.className='field-input';label.append(input);submit.className='control primary';submit.textContent=t('startup.connect');error.className='inline-error';error.setAttribute('role','alert');form.append(heading,label,error,submit);root.replaceChildren(form);
    form.onsubmit=async event=>{event.preventDefault();submit.disabled=true;error.textContent='';try{await exchange(input.value.trim());try{await tokenStore('put',input.value.trim());}catch{}input.value='';root.replaceChildren();stopViewport();resolve({...status,authenticated:true});}catch(reason){error.textContent=t('startup.accessFailed');console.error('Pot access:',reason);}finally{submit.disabled=false;}};
  });
}
export async function forgetBrowserAccess() {try{await disableNotificationInstallation();}catch{/* Server session revocation remains authoritative when local storage is blocked. */}try{await tokenStore('remove');}catch{}try{await fetch('/access/logout',{method:'POST',credentials:'same-origin'});}finally{try{await clearBusinessCaches();localStorage.removeItem('pot-ui-read-v1');}finally{location.reload();}}}
export async function configureBrowserAccess(token) {await exchange(token.trim());try{await tokenStore('put',token.trim());}catch{}}
export async function requestPWAInstall() {
  if(!installationPrompt)return false;
  const prompt=installationPrompt;installationPrompt=null;await prompt.prompt();await prompt.userChoice;return true;
}
export async function installPWA() {
  if('serviceWorker'in navigator&&isSecureContext)return navigator.serviceWorker.register('/service-worker.js',{scope:'/',type:'module',updateViaCache:'none'});
  return null;
}
