import {translate} from '../../locales/messages.mjs';
import {projectLinkedAssistants} from '../agent-links.mjs';
import {machinePresentation} from '../machine-identity.mjs';
const retained=item=>({...item,executing:false,pending_approvals:0,capabilities:{version:1,operations:['chat.read']},retained:true});
export function createDirectoryDomain(client,{resources,queries,commands}){
  let previous=[];
  const local=(value,space,offline)=>{let items=value;if(typeof items==='string')items=JSON.parse(items);const fresh=!resources.state(value)||resources.state(value).freshness==='fresh';return (items?.items||items||[]).map(item=>({...(!fresh?retained(item):item),control_id:space.control_id||space.id,space_id:space.id,space_name:space.name,space_kind:space.kind,offline}));};
  const directory=queries.define('directory',async force=>{
    if(client.startup&&client.onlineReady!==false&&!client.operationsReady)force=true;
    const generation=client.connectionGeneration;
    const current=()=>{if(generation!==client.connectionGeneration)throw new DOMException('Directory connection retired','AbortError');};
    const catalog=await resources.read('/api/v1/spaces',{owner:'',space:'',authoritative:force}),spaces=catalog.items||[];
    if(client.startup&&client.onlineReady!==false&&spaces.some(space=>{const before=client.spaces?.find(item=>item.id===space.id);return before?.control_id&&before.control_id!==space.control_id;})){
      client.revalidateStartup?.({resetScope:true});throw Object.assign(Error(translate(client.uiLocale?.()||'en','identityChanged')),{uiKey:'identityChanged'});
    }
    current();client.spaces=spaces;
    if(!spaces.length)throw Error('No serving Space');
    let partial=false;const confirmedSpaces=new Set();
    const results=await Promise.all(spaces.map(async space=>{try{
      const [items]=await Promise.all([resources.read('/api/v1/assistants',{space:space.id,owner:'',authoritative:force}),machinePresentation(client).load({space:space.id})]);
      const own=local(items,space,space.connected===false||client.onlineReady===false);
      if(force===true&&space.connected!==false)confirmedSpaces.add(space.id);
      try{const links=await resources.read('/api/v1/agent-links',{space:space.id,owner:'',authoritative:force}),fresh=!resources.state(links)||resources.state(links).freshness==='fresh';const linked=projectLinkedAssistants(links,space).map(item=>client.onlineReady===false?{...retained(item),offline:true}:fresh?item:retained(item));
        for(const item of previous.filter(item=>item.origin&&item.space_id===space.id&&!linked.some(current=>current.id===item.id))){if(links.items?.some(link=>link.id===item.origin.link_id&&link.status!=='connected'))linked.push({...item,offline:true});}return [...own,...linked];
      }catch(error){if(error.storage||error.name==='AbortError')throw error;partial=true;return [...own,...previous.filter(item=>item.origin&&item.space_id===space.id).map(item=>({...retained(item),offline:true}))];}
    }catch(error){if(error.storage||error.name==='AbortError')throw error;partial=true;const cached=previous.filter(item=>item.space_id===space.id);return (cached.length?cached:local(space.assistants||[],space,true)).map(item=>({...retained(item),offline:true}));}}));
    current();previous=results.flat();client.assistants=previous;
    // Only this connection's explicit network refresh can grant authority.
    // Failed Spaces and retained linked assistants cannot borrow another scope's
    // confirmation; an unavailable peer does not block confirmed local work.
    if(force===true&&client.onlineReady!==false){client.directorySpaces=confirmedSpaces;client.directoryGeneration=generation;}
    return {data:{spaces,assistants:previous,activeSpace:catalog.active_space_id||spaces[0].id,partial},freshness:partial?'stale':previous.some(item=>item.retained)?'retained':'fresh'};
  },{invalidationForce:false,topics:['spaces','assistants','assistant-mail','agent-links','conversation','runtime','permissions','tasks','runner','machines']});
  return {watch:listener=>directory.watch(listener),query:()=>directory.snapshot.data?Promise.resolve(directory.snapshot.data):directory.value(),refresh:()=>directory.refresh(true),
    async confirm(item){const space=client.spaces.find(space=>space.id===item.space_id);if(!space||space.connected===false)return {...item,offline:true};const value=await resources.read(item.origin?'/api/v1/agent-links':'/api/v1/assistants',{space:space.id,owner:'',authoritative:true});const items=item.origin?projectLinkedAssistants(value,space):local(value,space,false);return items.find(current=>current.id===item.id)||(item.origin&&value.items?.some(link=>link.id===item.origin.link_id&&link.status!=='connected')?{...item,offline:true}:null);},
    create:(scope,{profile,model,...values},requestID)=>commands.run(scope,'assistant:create',{...values,selection:{profile_id:profile.id,profile_revision:profile.revision,model}},(body,id,captured)=>client.request('/api/v1/assistants',{...captured,method:'POST',body:{...body,request_id:id}}),{requestID,idempotent:true}),
    update:(scope,id,values)=>commands.run(scope,'assistant:update:'+id,values,(body,_,captured)=>client.request('/api/v1/assistants/'+encodeURIComponent(id),{...captured,requiredCapabilities:['assistant.configure','assistant.profile'],method:'POST',body})),
    remove:(scope,id,revision)=>commands.run(scope,'assistant:remove:'+id,{revision},(body,_,captured)=>client.request('/api/v1/assistants/'+encodeURIComponent(id)+'/delete',{...captured,requiredCapabilities:['assistant.delete'],method:'POST',body})),
    profile:(scope,signal)=>client.request('/api/v1/assistants/profile',{...scope,signal}),
    defaultAvatar:scope=>client.request('/api/v1/assistants/default-avatar',scope),
    configuration:(scope,signal)=>client.request('/api/v1/agent',{...scope,signal}),
    instructions:(scope,signal)=>client.request('/api/v1/instructions',{...scope,signal}),
    configure:(scope,input)=>commands.run(scope,'assistant:configure',input,(body,_,captured)=>client.request('/api/v1/agent',{...captured,requiredCapabilities:['assistant.configure'],method:'POST',body})),
    saveInstructions:(scope,input)=>commands.run(scope,'assistant:instructions',input,(body,_,captured)=>client.request('/api/v1/instructions',{...captured,requiredCapabilities:['assistant.configure'],method:'POST',body})),
  };
}
