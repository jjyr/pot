import {translate} from '../locales/messages.mjs';
import {node as n,action,field} from './primitives.mjs';

// The URL is a resource endpoint. OAuth confirms receiver participation through
// the existing browser login; connecting never creates or selects an assistant.
export function createPotMCPConnections({root,request,locale,scope=()=>({}),signal,onConnected=()=>{}}){
 const t=key=>translate(locale(),key),section=n('section','agent-links'),heading=n('h3'),hint=n('p','hint'),form=n('form','editor-form'),url=field('', '',{name:'url',type:'url'}),connect=action('',()=>{}, {primary:true}),error=n('p','inline-error'),list=n('div','agent-link-list');
 connect.type='submit';url.input.type='url';url.input.required=true;url.input.autocomplete='url';error.setAttribute('role','alert');form.append(url.row,connect);section.append(heading,hint,form,error,list);root.prepend(section);
 let items=[],busy=false,disposed=false,generation=0,failed=false;
 const active=()=>!disposed&&!signal?.aborted&&root.isConnected;
 const call=(body)=>request('/api/v1/agent-links/mcp',{...(typeof scope==='function'?scope():scope),signal,...(body?{method:'POST',body}:{})});
 const redirect=()=>{const url=new URL('/mcp-pot-callback.html',location.href),current=typeof scope==='function'?scope():scope;if(current.space)url.searchParams.set('space_id',current.space);return url.href;};
 async function authorize(id){const value=await call({action:'authorize',id,url:id?'':url.input.value.trim(),redirect_uri:redirect()});if(active())location.assign(value.authorization_url);}
 async function reauthorize(item){if(busy)return;busy=true;failed=false;paint();try{await authorize(item.id);}catch{failed=true;}finally{busy=false;paint();}}
 async function change(item,command){if(busy)return;busy=true;paint();try{await call({action:command,id:item.id});await load();await onConnected(null);}catch{if(active()){failed=true;paint();}}finally{busy=false;paint();}}
 function paint(){if(!active())return;error.textContent=failed?t('potMCP.failed'):'';heading.textContent=t('potMCP.title');hint.textContent=t('potMCP.description');url.row.querySelector('.form-label').textContent=t('potMCP.url');url.input.setAttribute('aria-label',t('potMCP.url'));url.input.placeholder=t('potMCP.placeholder');connect.textContent=t('potMCP.connect');connect.disabled=busy;
  list.replaceChildren();if(!items.length)list.append(n('p','hint',t('potMCP.empty')));
  for(const item of items){const row=n('div','agent-link-row'),info=n('div','row-info'),buttons=n('div','page-toolbar');info.append(n('strong','',item.directory?.name||item.url),n('code','',item.remote_node_id||item.url),n('span','hint',t('potMCP.status.'+item.status)));if(item.last_error)info.append(n('p','hint',t(item.last_error==='authorization_expired'?'potMCP.expired':'potMCP.offlineHint')));
   const expired=item.last_error==='authorization_expired';if(item.inbound)info.append(n('p','hint',t('potMCP.inbound')));if(item.state!=='revoked'){if(!item.inbound)buttons.append(action(t(expired?'potMCP.authorize':item.state==='disconnected'?'potMCP.reconnect':'potMCP.disconnect'),()=>expired?reauthorize(item):change(item,item.state==='disconnected'?'reconnect':'disconnect')));buttons.append(action(t('potMCP.revoke'),()=>change(item,'revoke'),{danger:true}));}for(const button of buttons.querySelectorAll('button'))button.disabled=busy;
   row.append(info,buttons);list.append(row);
  }
 }
 async function load(){const current=++generation;try{const value=await call();if(active()&&current===generation){items=value;failed=false;paint();}return value;}catch{if(active()&&current===generation){failed=true;items=items.map(item=>item.state==='active'?{...item,status:'offline'}:item);paint();}}}
 form.addEventListener('submit',async event=>{event.preventDefault();if(busy)return;busy=true;failed=false;paint();try{await authorize('');}catch{if(active()){failed=true;paint();}}finally{busy=false;paint();}});
 const refresh=()=>paint(),timer=setInterval(()=>{if(active()&&!busy)load();},15000);window.addEventListener('pot:locale',refresh);paint();load();
 const dispose=()=>{if(disposed)return;disposed=true;generation++;clearInterval(timer);window.removeEventListener('pot:locale',refresh);section.remove();};signal?.addEventListener('abort',dispose,{once:true});
 return {load,dispose};
}
