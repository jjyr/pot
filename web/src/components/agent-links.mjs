import {createPotMCPConnections} from './pot-mcp-connections.mjs';
import {linkDisplayName,linkHostLabel} from '../state/machine-identity.mjs';
import {translate} from '../locales/messages.mjs';
import {node as n} from './primitives.mjs';
import {connectionBrand} from './connection-visuals.mjs';

// Legacy rows expose saved history only. Connection setup belongs to MCP OAuth.
export function createAgentLinks({root,request,resources,locale,scope=()=>({}),onConnected=()=>{},signal,showConnections=false}) {
  const potMCP=createPotMCPConnections({root,request,resources,locale,scope,signal,onConnected});
  const section=n('section','agent-links'),list=n('div','agent-link-list'),error=n('p','inline-error');
  error.setAttribute('role','alert');section.append(error,list);root.append(section);
  let snapshot=null,disposed=false,version=0;
  const active=()=>!disposed&&!signal?.aborted&&root.isConnected;
  const captured=()=>typeof scope==='function'?scope():scope;
  function sync(){
    if(!active())return;
    const t=key=>translate(locale(),key);
    list.hidden=!showConnections;list.replaceChildren();
    if(!showConnections)return;
    for(const link of (snapshot?.items||[]).filter(item=>item.transport!=='mcp')){
      const row=n('div','agent-link-row'),info=n('div','row-info');
      const state=link.last_known_state||link.status;
      info.append(n('strong','',linkDisplayName(link)),n('span','hint',linkHostLabel(link)||t('machine.unavailable')),n('span','hint',t('potMCP.legacy')),n('span','hint',t('potMCP.lastState').replace('{state}',t(state==='disconnected'?'agentLinks.disconnected':'agentLinks.status.'+(['connected','connecting'].includes(state)?state:'offline')))));
      row.append(connectionBrand(link.peer?.kind),info);list.append(row);
    }
  }
  async function load(){
    const generation=++version,current={...captured()},mcpRead=potMCP.load();
    try{const value=await request('/api/v1/agent-links',{...current,signal});if(active()&&generation===version&&JSON.stringify(captured())===JSON.stringify(current)){snapshot=value;error.textContent='';sync();}await mcpRead;return value;}
    catch(failure){if(active()&&generation===version&&JSON.stringify(captured())===JSON.stringify(current)&&failure.name!=='AbortError')error.textContent=translate(locale(),'agentLinks.failed');await mcpRead;}
  }
  function update(value){snapshot=value;sync();void potMCP.load();}
  function dispose(){disposed=true;version++;potMCP.dispose();section.remove();}
  signal?.addEventListener('abort',dispose,{once:true});
  async function change(link,command){
    const item=potMCP.items.find(item=>item.id===link.id);
    if(!item||link.peer?.node_id&&link.peer.node_id!==item.remote_node_id){error.textContent=translate(locale(),'potMCP.failed');return;}
    return potMCP.change(item,command);
  }
  return {load,update,sync,dispose,disconnect:link=>change(link,'disconnect'),reconnect:link=>change(link,'reconnect'),remove:link=>change(link,'revoke'),get snapshot(){return snapshot;}};
}
