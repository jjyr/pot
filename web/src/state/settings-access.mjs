import {supportsCapability} from './agent-links.mjs';

const identity=item=>JSON.stringify([item?.control_id,item?.space_id,item?.id]);
const clients=new WeakMap();
export const configurationRoute=route=>route==='settings'||route==='machines';
// Remember presentation support only. A retained directory can keep an editor
// discoverable, but can never authorize a mutation.
export function createSettingsAccess(client){
  if(client&&clients.has(client))return clients.get(client);
  const known=new Map();
  const remember=item=>{if(!known.has(identity(item)))known.set(identity(item),item.capabilities);};
  const definition=(item,assistants=[])=>{
    const current=assistants.find(value=>identity(value)===identity(item));
    if(!current)return null;
    if(!current.retained)known.set(identity(current),current.capabilities);
    return {...current,capabilities:current.retained?known.get(identity(current)):current.capabilities};
  };
  const access=Object.assign((operation,item,assistants)=>supportsCapability(definition(item,assistants),operation),{definition,remember});
  if(client)clients.set(client,access);return access;
}

export function captureConfigurationScope(client,scope){
  const space=client.spaces?.find(item=>item.id===scope.space);
  return {...scope,configuration:true,configurationBoot:client.runtime.boot_id,configurationRuntime:client.runtime.runtime_id||client.runtime.id,
    control_id:scope.control_id??space?.control_id??space?.id};
}

export function configurationIdentityCurrent(client,scope){
  return scope.configurationBoot===client.runtime.boot_id&&scope.configurationRuntime===(client.runtime.runtime_id||client.runtime.id)&&
    ((!client.production&&!scope.control_id)||client.spaces?.some(item=>item.id===scope.space&&(item.control_id||item.id)===scope.control_id));
}
