import {communicationArgs} from './communication.mjs';

// Presentation only: never use directory names or synthetic UI IDs for routing.
const presentations=new WeakMap();
const addressKey=address=>address?.node_id&&address?.kind&&address?.agent_id?JSON.stringify([address.node_id,address.kind,address.agent_id]):null;
export function messageAddresses(tool){
  const args=communicationArgs(tool);
  if(tool?.name==='send_message')return [args?.to];
  if(tool?.name==='receive_message')return Array.isArray(args?.messages)?args.messages.map(message=>message?.from):[];
  return null;
}
export function messageIdentities(client){
  if(presentations.has(client))return presentations.get(client);
  let directory,epoch,index=new Map();
  const cache=new Map();
  const presentation={actor(scope){
    const control=(client.spaces||[]).find(item=>item.id===scope?.space)?.control_id||scope?.space;
    const item=(client.assistants||[]).find(item=>item.id===scope?.owner&&item.control_id===control);
    return item?.origin?{node_id:item.origin.node_id,kind:'assistant',agent_id:item.origin.assistant_id}:item?.control_id&&item?.id?{node_id:item.control_id,kind:'assistant',agent_id:item.id}:undefined;
  },resolve(address){
    const nextEpoch=JSON.stringify([client.runtime?.runtime_id,client.runtime?.boot_id]);
    if(epoch!==nextEpoch){epoch=nextEpoch;directory=undefined;index=new Map();cache.clear();}
    if(directory!==client.assistants){
      directory=client.assistants;index=new Map();
      for(const item of directory||[]){
        const source=item.origin;
        const identity={node_id:source?source.node_id:item.control_id,kind:'assistant',agent_id:source?source.assistant_id:item.id};
        // Keep the actual linked profile name separate from the directory's ID fallback.
        const key=addressKey(identity),profileName=source?item.profile_name:item.name;
        const name=typeof profileName==='string'?profileName.trim():'';
        if(!key||!name)continue;
        const profile={name,origin:source,avatar:item.avatar,brand:source?.kind||(!source?'pot':undefined),offline:!!item.offline};index.set(key,profile);
        cache.delete(key);cache.set(key,profile);
        if(cache.size>512)cache.delete(cache.keys().next().value);
      }
    }
    return address?.kind==='assistant'?(index.get(addressKey(address))||cache.get(addressKey(address))):undefined;
  }};
  presentations.set(client,presentation);return presentation;
}
