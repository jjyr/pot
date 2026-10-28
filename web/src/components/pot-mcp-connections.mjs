import {translate} from '../locales/messages.mjs';
import {node as n,action,field} from './primitives.mjs';
import {keyedRows} from './reconcile.mjs';

// Retained rows explain history. Every command separately confirms the current
// receiver, and OAuth responses never enter the display repository.
export function createPotMCPConnections({root,request,resources,locale,scope=()=>({}),signal,onConnected=()=>{}}){
  const path='/api/v1/agent-links/mcp',t=key=>translate(locale(),key);
  const section=n('section','agent-links'),heading=n('h3'),hint=n('p','hint'),form=n('form','editor-form mcp-connection-form');
  const url=field('','',{name:'url',type:'url'}),connect=action('',()=>{},{primary:true});
  const error=n('p','inline-error'),errorLabel=n('span'),retry=action('',()=>load()),empty=n('p','hint'),list=n('div','agent-link-list'),entries=new Map();
  connect.type='submit';url.input.type='url';url.input.required=true;url.input.autocomplete='url';
  error.setAttribute('role','alert');error.append(errorLabel,retry);form.append(url.row,connect);section.append(heading,hint,form,error,empty,list);root.prepend(section);
  let items=[],epoch=0,scopeID='',inflight=null,disposed=false,busy=false,failed=false,failureKey='potMCP.failed',known=false,fresh=false,hydrated=false;
  const active=()=>!disposed&&!signal?.aborted&&root.isConnected;
  const capture=()=>{const value=typeof scope==='function'?scope():scope;return {space:value.space||'',owner:value.owner||''};};
  const key=value=>JSON.stringify([value.space,value.owner,resources?.describe?.(path,value).control||'']);
  const current=(captured,version)=>active()&&version===epoch&&key(capture())===scopeID;
  function select(){
    const captured=capture(),id=key(captured);
    if(id!==scopeID){scopeID=id;epoch++;inflight=null;items=[];known=false;fresh=false;hydrated=false;busy=false;failed=false;paint();}
    return {captured,version:epoch};
  }
  const read=captured=>resources?.read?resources.read(path,{...captured,signal,authoritative:true,notify:false}):request(path,{...captured,signal,authoritative:true,notify:false});
  const values=value=>Array.isArray(value)?value:[];
  const commandFor=item=>item.last_error==='authorization_expired'?'authorize':item.state==='disconnected'?'reconnect':'disconnect';
  const allowed=(item,command)=>item?.state!=='revoked'&&(command==='revoke'||!item?.inbound&&(command==='authorize'?item?.last_error==='authorization_expired':command==='reconnect'?item?.state==='disconnected'&&item?.last_error!=='authorization_expired':command==='disconnect'&&item?.state==='active'&&item?.last_error!=='authorization_expired'));
  const identity=(a,b)=>a.id===b.id&&a.remote_node_id===b.remote_node_id&&a.url===b.url&&!!a.inbound===!!b.inbound&&a.state===b.state;
  function createRow(item){
    const element=n('div','agent-link-row'),info=n('div','row-info'),buttons=n('div','page-toolbar');
    const title=n('strong'),address=n('code'),status=n('span','hint'),diagnostic=n('p','hint'),inbound=n('p','hint');
    const entry={element,title,address,status,diagnostic,inbound,item};
    entry.primary=action('',()=>change(entry.item,commandFor(entry.item)));
    entry.revoke=action('',()=>change(entry.item,'revoke'),{danger:true});
    info.append(title,address,status,diagnostic,inbound);buttons.append(entry.primary,entry.revoke);element.append(info,buttons);return entry;
  }
  function paint(){
    if(!active())return;
    heading.textContent=t('potMCP.title');hint.textContent=t('potMCP.description');
    url.row.querySelector('.form-label').textContent=t('potMCP.url');url.input.setAttribute('aria-label',t('potMCP.url'));url.input.placeholder=t('potMCP.placeholder');
    connect.textContent=t('potMCP.connect');connect.disabled=busy;
    error.hidden=!failed;errorLabel.textContent=failed?t(failureKey):'';retry.textContent=t('retry');retry.disabled=busy;
    empty.hidden=!known||items.length>0;empty.textContent=t('potMCP.empty');
    keyedRows(list,entries,items,{key:item=>item.id,create:createRow,update:(entry,item)=>{
      entry.title.textContent=item.directory?.name||item.url||item.remote_node_id||'';
      entry.address.textContent=item.remote_node_id||item.url||'';
      const status=t('potMCP.status.'+(['connected','active','offline','disconnected','revoked'].includes(item.status)?item.status:'offline'));
      entry.status.textContent=fresh?status:t('potMCP.lastState').replace('{state}',status);
      entry.diagnostic.hidden=!item.last_error;entry.diagnostic.textContent=item.last_error?t(item.last_error==='authorization_expired'?'potMCP.expired':'potMCP.offlineHint'):'';
      entry.inbound.hidden=!item.inbound;entry.inbound.textContent=t('potMCP.inbound');
      entry.primary.hidden=item.inbound||item.state==='revoked';entry.primary.textContent=t('potMCP.'+commandFor(item));
      entry.revoke.hidden=item.state==='revoked';entry.revoke.textContent=t('potMCP.revoke');
      entry.primary.disabled=busy;entry.revoke.disabled=busy;
    }});
  }
  function load(){
    if(!active())return Promise.resolve();
    const {captured,version}=select();if(inflight)return inflight;
    const pending=(async()=>{
      try{
        if(!hydrated&&resources?.query){
          const saved=await resources.query(path,captured);if(!current(captured,version))return;
          hydrated=true;if(saved){items=values(saved.value);known=true;fresh=false;paint();}
        }
        const value=await read(captured);if(!current(captured,version))return;
        items=values(value);known=true;fresh=true;failed=false;paint();return items;
      }catch(reason){if(current(captured,version)){failed=true;failureKey=reason?.storage?'storage.failed':'potMCP.failed';fresh=false;paint();}}
    })().finally(()=>{if(inflight===pending)inflight=null;});inflight=pending;return pending;
  }
  async function change(target,command){
    if(!active())return;
    // A row from a retired Space must never be reinterpreted in the new Space.
    if(target&&key(capture())!==scopeID){select();return;}
    const {captured,version}=select();if(busy)return;
    const expected=target?{...target}:null,input=url.input.value.trim();
    if(expected&&!allowed(expected,command))return;
    busy=true;failed=false;paint();let navigating=false;
    try{
      const value=values(await read(captured));if(!current(captured,version))return;
      items=value;known=true;fresh=true;
      if(expected){const live=value.find(item=>item.id===expected.id);if(!live||!identity(expected,live)||!allowed(live,command))throw Error('Connection changed');}
      const callback=new URL('/mcp-pot-callback.html',location.href);if(captured.space)callback.searchParams.set('space_id',captured.space);
      const body=command==='authorize'?{action:command,id:expected?.id||'',url:expected?'':input,redirect_uri:callback.href}:{action:command,id:expected.id};
      const result=await request(path,{...captured,signal,transient:true,method:'POST',body});if(!current(captured,version))return;
      if(command==='authorize'){if(typeof result.authorization_url!=='string'||!result.authorization_url)throw Error('Missing authorization destination');location.assign(result.authorization_url);navigating=true;return;}
      // A pre-command read cannot hide the result of a successful mutation.
      await inflight;if(!current(captured,version))return;
      await load();if(!current(captured,version))return;
      await onConnected(result);
    }catch(reason){if(current(captured,version)){failed=true;failureKey=reason?.storage?'storage.failed':'potMCP.failed';fresh=false;}}
    finally{if(current(captured,version)&&!navigating){busy=false;paint();}}
  }
  form.addEventListener('submit',event=>{event.preventDefault();void change(null,'authorize');});
  const refresh=()=>paint();
  window.addEventListener('pot:locale',refresh);
  const dispose=()=>{if(disposed)return;disposed=true;epoch++;window.removeEventListener('pot:locale',refresh);section.remove();};
  signal?.addEventListener('abort',dispose,{once:true});paint();void load();
  return {load,change,dispose,get items(){return items;}};
}
