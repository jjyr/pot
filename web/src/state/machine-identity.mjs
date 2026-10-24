import {translate} from '../locales/messages.mjs';

export const machineDisplayName=machine=>machine?.hostname||([machine?.id,machine?.runtime_id].includes(machine?.name)?'':machine?.name)||machine?.ip_addresses?.[0]||machine?.os||'';
export const machineHostLabel=machine=>[machineDisplayName(machine),machine?.ip_addresses?.[0]].filter((value,index,all)=>value&&all.indexOf(value)===index).join(' · ');
export function linkEndpoint(link){
  try{const url=new URL(link?.peer?.address);return url.protocol==='tcp:'&&!url.username&&!url.password&&!url.search&&!url.hash?url.host:'';}catch{return '';}
}
export const linkHostLabel=link=>[link?.observation?.value?.hostname,linkEndpoint(link)].filter((value,index,all)=>value&&all.indexOf(value)===index).join(' · ');
export const linkDisplayName=link=>link?.peer?.name&&link.peer.name!==link.peer.node_id?link.peer.name:link?.peer?.kind==='antigravity'?'Antigravity':link?.peer?.kind==='codex'?'Codex':'Pot';

// The graph and execution labels project a single host from its owning runtime.
export const runtimeMachine=runtime=>runtime?.runtime_id&&runtime.host?{...runtime.host,id:runtime.runtime_id,runtime_id:runtime.runtime_id,connected:true}:null;

const clients=new WeakMap();
// Presentation samples are scoped to the owning Control, never used for routing.
export function machinePresentation(client){
  if(clients.has(client))return clients.get(client);
  const inventory=new Map(),key=space=>{const owner=(client.spaces||[]).find(item=>item.id===space);return JSON.stringify([space,owner?.control_id,owner?.boot_id||client.runtime?.boot_id]);};
  const value={
    revision:0,
    async load(scope={}){
      const space=scope.space||client.scope?.().space||'',identity=key(space);
      try{const runtime=await (client.read||client.request)('/api/v1/runtime',{...scope,space,owner:''});if(identity===key(space)){const machine=runtimeMachine(runtime),items=machine?[machine]:[];if(JSON.stringify(items)!==JSON.stringify(inventory.get(identity)))value.revision++;inventory.set(identity,items);}}catch(error){if(scope.signal?.aborted)throw error;/* Retain the last host metadata while offline. */}
      return inventory.get(identity)||[];
    },
    label(id,scope,locale){if(!id)return '';const source=(client.assistants||[]).find(item=>item.id===scope?.owner&&item.space_id===scope?.space)?.origin;if(source)return source.host||translate(locale,'machine.unavailable');const machine=(inventory.get(key(scope?.space||client.scope?.().space||''))||[]).find(item=>(item.runtime_id||item.id)===id);return machineHostLabel(machine)||translate(locale,'machine.unavailable');},
  };
  clients.set(client,value);return value;
}
