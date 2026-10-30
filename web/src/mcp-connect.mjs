import {translate} from './locales/messages.mjs';
import {storedPreference} from './state/preferences.mjs';
import {ensureBrowserAccess} from './adapters/access.mjs';
import {getBusinessSocket} from './adapters/ws-client.mjs';

const root=document.querySelector('#connection');
const locale=()=>storedPreference('pot-ui-locale',navigator.language.startsWith('zh')?'zh-CN':'en');
const t=key=>translate(locale(),key);
function element(tag,text){const node=document.createElement(tag);if(text)node.textContent=text;return node;}
let busy=false;
const failure=element('p');failure.setAttribute('role','alert');failure.hidden=true;
function button(key,action){
 const node=element('button',t(key));node.className='control';
 node.onclick=async()=>{if(busy)return;busy=true;failure.hidden=true;const controls=[...root.querySelectorAll('button')];for(const control of controls)control.disabled=true;
  let navigating=false;
  try{navigating=await action()===true;}catch{failure.textContent=t('mcpAccess.failed');failure.hidden=false;root.append(failure);}
  finally{if(!navigating){busy=false;for(const control of controls)control.disabled=false;}}
 };return node;
}
async function api(path,body){return getBusinessSocket().request(path,body?{method:'POST',body}:{});}
async function start(){
 await ensureBrowserAccess(root);
 const params=new URLSearchParams(location.search),returnTo=params.get('return_to');
 if(returnTo){const target=new URL(returnTo,location.origin);if(target.origin!==location.origin||target.pathname!=='/oauth/authorize')throw Error('Invalid return destination');location.replace(target.href);return;}
 const request=params.get('request');
 root.replaceChildren(element('h1',t(request?'mcpAccess.title':'mcpAccess.connections')));
 if(request){
  const details=await api('/oauth/consent?'+new URLSearchParams({request}));
  root.append(element('p',details.client_name||t('mcpAccess.client')),element('p',t('mcpAccess.description')));
  if(details.receiver_node){root.append(element('p',t('mcpAccess.receiver')),element('code',details.receiver_node));}
  const decide=async approve=>{const result=await api('/oauth/consent',{request_id:request,approve});location.assign(result.redirect);return true;};
  root.append(element('p',t('mcpAccess.connectionScope')),button('mcpAccess.allow',()=>decide(true)),button('mcpAccess.deny',()=>decide(false)));return;
 }
 const connections=await api('/oauth/connections');
 if(!connections.length)root.append(element('p',t('mcpAccess.empty')));
 for(const item of connections){const row=element('section');row.append(element('h2',item.client_name||t('mcpAccess.client')),element('p',t(item.revoked_at?'mcpAccess.revoked':!item.expires_at?'mcpAccess.pending':item.expires_at*1000<=Date.now()?'mcpAccess.expired':'mcpAccess.connected')));if(item.expires_at)row.append(element('p',t('mcpAccess.expires').replace('{time}',new Intl.DateTimeFormat(locale(),{dateStyle:'medium',timeStyle:'short'}).format(new Date(item.expires_at*1000)))));if(item.receiver_node)row.append(element('code',item.receiver_node));if(!item.revoked_at)row.append(button('mcpAccess.revoke',async()=>{await api('/oauth/connections',{connection_id:item.connection_id});await start();}));root.append(row);}
}
start().catch(()=>root.replaceChildren(element('p',t('mcpAccess.failed'))));
