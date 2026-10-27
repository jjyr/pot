import {translate} from './locales/messages.mjs';
import {storedPreference} from './state/preferences.mjs';
import {ensureBrowserAccess} from './adapters/access.mjs';

const root=document.querySelector('#connection');
const locale=storedPreference('pot-ui-locale',navigator.language.startsWith('zh')?'zh-CN':'en');
const t=key=>translate(locale,key);
function element(tag,text){const node=document.createElement(tag);if(text)node.textContent=text;return node;}
function button(key,action){const node=element('button',t(key));node.className='control';node.onclick=async()=>{node.disabled=true;try{await action();}catch{root.append(element('p',t('mcpAccess.failed')));}finally{node.disabled=false;}};return node;}
async function api(path,body){const response=await fetch(path,{credentials:'same-origin',...(body?{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}:{})});if(!response.ok)throw Error('MCP connection request failed');return response.json();}
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
  const decide=async approve=>{const result=await api('/oauth/consent',{request_id:request,approve});location.assign(result.redirect);};
  root.append(element('p',t('mcpAccess.connectionScope')),button('mcpAccess.allow',()=>decide(true)),button('mcpAccess.deny',()=>decide(false)));return;
 }
 const connections=await api('/oauth/connections');
 if(!connections.length)root.append(element('p',t('mcpAccess.empty')));
 for(const item of connections){const row=element('section');row.append(element('h2',item.client_name||t('mcpAccess.client')),element('p',t(item.revoked_at?'mcpAccess.revoked':'mcpAccess.connected')));if(item.receiver_node)row.append(element('code',item.receiver_node));if(!item.revoked_at)row.append(button('mcpAccess.revoke',async()=>{await api('/oauth/connections',{connection_id:item.connection_id});await start();}));root.append(row);}
}
start().catch(()=>root.replaceChildren(element('p',t('mcpAccess.failed'))));
