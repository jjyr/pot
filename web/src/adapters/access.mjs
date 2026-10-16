// The installation token is origin-scoped in IndexedDB. API calls use the
// server's HttpOnly session cookie; credentials never enter routes or logs.
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
async function exchange(token) {
  const response=await fetch('/access/token',{method:'POST',credentials:'same-origin',headers:{Authorization:`Bearer ${token}`}});
  if(!response.ok)throw Error(response.status===401?'Access token was rejected.':'Could not configure browser access.');
}
export async function ensureBrowserAccess(root) {
  const fragment=takeAccessFragment();
  const response=await fetch('/access/status',{credentials:'same-origin'});
  if(response.status===404)return null; // Isolated fixture is checked by bootstrap.
  if(!response.ok)throw Error(`Browser access: HTTP ${response.status}`);
  const status=await response.json();
  let saved=fragment;
  if(!saved)try{saved=await tokenStore('get');}catch{/* A restricted browser can use a session-only token. */}
  if(fragment||!status.authenticated&&saved) {
    try {await exchange(saved);try{await tokenStore('put',saved);}catch{}return {...status,authenticated:true};}
    catch {try{await tokenStore('remove');}catch{}if(status.authenticated&&!fragment)return status;}
  }
  if(status.authenticated&&!fragment)return status;
  return new Promise(resolve=>{
    const zh=navigator.language.startsWith('zh'),form=document.createElement('form'),heading=document.createElement('h1'),label=document.createElement('label'),input=document.createElement('input'),submit=document.createElement('button'),error=document.createElement('p');
    root.className='access-setup';heading.textContent=zh?'连接到 Pot':'Connect to Pot';label.textContent=zh?'访问令牌':'Access token';input.type='password';input.autocomplete='off';input.required=true;input.setAttribute('aria-label',label.textContent);input.className='field-input';label.append(input);submit.className='control primary';submit.textContent=zh?'连接':'Connect';error.className='inline-error';error.setAttribute('role','alert');form.append(heading,label,error,submit);root.replaceChildren(form);
    form.onsubmit=async event=>{event.preventDefault();submit.disabled=true;error.textContent='';try{await exchange(input.value.trim());try{await tokenStore('put',input.value.trim());}catch{}input.value='';root.replaceChildren();resolve({...status,authenticated:true});}catch(reason){error.textContent=reason.message;}finally{submit.disabled=false;}};
  });
}
export async function forgetBrowserAccess() {try{await tokenStore('remove');}catch{}await fetch('/access/logout',{method:'POST',credentials:'same-origin'});location.reload();}
export async function configureBrowserAccess(token) {await exchange(token.trim());try{await tokenStore('put',token.trim());}catch{}}
export async function requestPWAInstall() {
  if(!installationPrompt)return false;
  const prompt=installationPrompt;installationPrompt=null;await prompt.prompt();await prompt.userChoice;return true;
}
export async function installPWA() {
  if('serviceWorker'in navigator&&isSecureContext)return navigator.serviceWorker.register('/service-worker.js',{scope:'/'});
  return null;
}
export async function configurePush(status,{enabled=true,locale='en',private_preview=false}={}) {
  if(!status?.push_available||!status.push_key)throw Error('Push notifications are unavailable on this server.');
  const registration=await navigator.serviceWorker.ready;
  if(!enabled){const subscription=await registration.pushManager.getSubscription();const response=await fetch('/access/push/remove',{method:'POST',credentials:'same-origin'});if(!response.ok)throw Error('Could not remove notifications.');await subscription?.unsubscribe();return;}
  if(await Notification.requestPermission()!=='granted')throw Error('Notification permission was not granted.');
  const key=Uint8Array.from(atob(status.push_key.replace(/-/g,'+').replace(/_/g,'/')),value=>value.charCodeAt(0));
  const subscription=await registration.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:key});
  const response=await fetch('/access/push',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({subscription:subscription.toJSON(),locale,private_preview})});
  if(!response.ok){await subscription.unsubscribe();throw Error('Could not save notification subscription.');}
}
