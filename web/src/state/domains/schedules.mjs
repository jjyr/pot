import {resourceAuthority,resourceAuthorityCurrent} from '../resource-authority.mjs';
import {translate} from '../../locales/messages.mjs';
import {supportsCapability} from '../agent-links.mjs';
export function createSchedulesDomain(client,{resources,queries,commands}){
  const manage=scope=>{
    const assistant=client.assistants?.find(item=>item.space_id===scope.space&&item.id===scope.owner);
    if(client.operationScopeReady?.(scope)===false||assistant?.retained||assistant?.offline||!supportsCapability(assistant,'schedules.manage'))throw Object.assign(Error(translate(client.uiLocale?.()||'en','agentLinks.capabilityUnavailable')),{uiKey:'agentLinks.capabilityUnavailable'});
  };
  const query=(scope,{selected,runsOffset=0,limit=20}={})=>queries.define('schedules:'+JSON.stringify([scope,selected,runsOffset,limit]),async force=>{
    const authority=resourceAuthority(client,scope,resources),data=await resources.read('/api/v1/schedules?'+new URLSearchParams({...(selected?{selected}:{}),runs_offset:runsOffset,limit}),{...scope,authoritative:force});
    if(!resourceAuthorityCurrent(client,scope,authority,resources))return {data,freshness:'retained',authority};
    return {data,authority};
  },{topics:['schedules','tasks','spaces','machines'],space:scope.space});
  const collection=()=>queries.define('schedules:list',async force=>{const pages=await Promise.all((client.spaces||[]).flatMap(space=>(client.assistants||[]).filter(item=>item.id&&item.space_id===space.id&&space.control_id).map(async assistant=>{const scope={space:space.id,owner:assistant.id},path='/api/v1/schedules?limit=50';try{if(!supportsCapability(assistant,'schedules.manage')||client.operationScopeReady?.(scope)===false){const saved=await resources.query(path,{...scope,collection:true});return saved?{space,owner:assistant.id,value:saved.value,stale:true}:null;}const value=await resources.collection(path,{...scope,authoritative:force},async()=>{const items=[],seen=new Set();let offset=0,page;do{if(seen.has(offset))throw Error('schedule_pagination');seen.add(offset);page=await (client.transport||client.request)('/api/v1/schedules?'+new URLSearchParams({limit:50,offset}),scope);items.push(...(page.items||[]));offset=page.next_offset;if(items.length>5000)throw Error('schedule_page_limit');}while(offset!=null);return {...page,items,next_offset:null};});return {space,owner:assistant.id,value,stale:resources.state(value)?.freshness!=='fresh'};}catch(error){const saved=await resources.query(path,{...scope,collection:true});if(!saved)throw error;return {space,owner:assistant.id,value:saved.value,stale:true,error};}})));const visible=pages.filter(Boolean);return {data:visible,freshness:visible.some(page=>page.stale)?'retained':'fresh'};},{topics:['schedules','tasks','assistants','spaces','machines']});
  const write=(scope,operation,input,requestID)=>{
    const authority=scope._authority||resourceAuthority(client,scope,resources);
    return commands.run(scope,'schedule:'+operation,input,(body,id,captured)=>{
      manage(captured);if(!resourceAuthorityCurrent(client,captured,authority,resources))throw Object.assign(Error(translate(client.uiLocale?.()||'en','identityChanged')),{uiKey:'identityChanged'});
      return client.request('/api/v1/schedules',{...captured,expectedControl:resources.describe?.('/api/v1/schedules',captured)?.control,requiredCapabilities:['schedules.manage'],method:'POST',body:{...body,request_id:id}});
    },{requestID,idempotent:true});
  };
  return {query,watch(scope,options,listener){const target=query(scope,options),publish=snapshot=>listener({...snapshot,pending:commands.pending(scope,'schedule:')});const stop=target.watch(publish),stopCommands=commands.watch(event=>{if(event.scope.space===scope.space&&event.scope.owner===scope.owner&&event.operation.startsWith('schedule:'))publish(target.snapshot);});return()=>{stop();stopCommands();};},refresh:(scope,options)=>query(scope,options).refresh(true),watchList:listener=>collection().watch(listener),refreshList:()=>collection().refresh(true),
    create:(scope,spec,requestID)=>write(scope,'create',{action:'create',schedule_id:'',spec},requestID),update:(scope,id,spec,requestID)=>write(scope,'save:'+id,{action:'update',schedule_id:id,spec},requestID),
    run:(scope,id)=>write(scope,'run:'+id,{action:'run',schedule_id:id}),pause:(scope,id,paused)=>write(scope,'pause:'+id,{action:paused?'pause':'resume',schedule_id:id}),remove:(scope,id)=>write(scope,'remove:'+id,{action:'delete',schedule_id:id}),
    preview:(scope,spec,signal)=>{manage(scope);return client.request('/api/v1/schedules/preview',{...scope,signal,expectedControl:resources.describe?.('/api/v1/schedules',scope)?.control,requiredCapabilities:['schedules.manage'],method:'POST',body:spec});},
  };
}
