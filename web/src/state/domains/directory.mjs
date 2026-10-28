import {translate} from '../../locales/messages.mjs';
import {projectLinkedAssistants} from '../agent-links.mjs';
import {machinePresentation} from '../machine-identity.mjs';
import {createSettingsAccess} from '../settings-access.mjs';
export function createDirectoryDomain(client,{resources,queries,commands}){
  let previous=[],previousGeneration,closed=false,refreshPending,batch=0;
  const metadataLoads=new Map(),activeStops=new Set();
  const settingsAccess=createSettingsAccess(client);
  // A background directory read revokes command capabilities, but it is not an
  // execution-finished event. Retain activity only from this live connection's
  // prior projection. Cold/offline/reconnected caches cannot invent activity.
  const retained=item=>{settingsAccess.remember(item);return {...item,executing:client.onlineReady!==false&&previousGeneration===client.connectionGeneration&&!!previous.find(old=>old.id===item.id&&old.space_id===item.space_id&&old.control_id===item.control_id&&!old.offline)?.executing,pending_approvals:0,capabilities:{version:1,operations:['chat.read']},retained:true};};
  const local=(value,space,offline)=>{let items=value;if(typeof items==='string')items=JSON.parse(items);const fresh=!resources.state(value)||resources.state(value).freshness==='fresh';return (items?.items||items||[]).map(item=>({...(!fresh?retained({...item,space_id:space.id,control_id:space.control_id||space.id}):item),control_id:space.control_id||space.id,space_id:space.id,space_name:space.name,space_kind:space.kind,offline}));};
  const firstReaders=new Set();
  const settleReaders=(data,error)=>{for(const reader of firstReaders)error?reader.reject(error):reader.resolve(data);firstReaders.clear();};
  const directory=queries.define('directory',async(force,publish)=>{
    const ticket=++batch;
    if(client.startup&&client.onlineReady!==false&&!client.operationsReady)force=true;
    const generation=client.connectionGeneration,revision=client.directoryAuthorityRevision||0,serving=JSON.stringify([client.runtime?.runtime_id,client.runtime?.boot_id]),controls=new Map((client.spaces||[]).map(space=>[space.id,space.control_id]));
    let failed=false,authorityRevision=revision,activeControls,backgroundError;
    const stopProgress=[];let refreshThis;
    const identityCurrent=()=>{if(closed||failed||generation!==client.connectionGeneration||serving!==JSON.stringify([client.runtime?.runtime_id,client.runtime?.boot_id])||activeControls&&[...activeControls].some(([id,control])=>client.spaces?.find(space=>space.id===id)?.control_id!==control))throw new DOMException('Directory connection retired','AbortError');};
    const current=()=>{identityCurrent();if(ticket!==batch)throw new DOMException('Directory batch retired','AbortError');if(backgroundError)throw backgroundError;};
    const revoke=()=>{if(generation===client.connectionGeneration&&authorityRevision===(client.directoryAuthorityRevision||0)&&serving===JSON.stringify([client.runtime?.runtime_id,client.runtime?.boot_id])&&[...controls].every(([id,control])=>client.spaces?.find(space=>space.id===id)?.control_id===control)){client.directoryGeneration=-1;client.directorySpaces=new Set();client.directoryAuthorityRevision=authorityRevision+1;}};
    const adoptSpaces=spaces=>{
      current();
      if(client.startup&&client.onlineReady!==false&&spaces.some(space=>{const before=client.spaces?.find(item=>item.id===space.id);return before?.control_id&&before.control_id!==space.control_id;})){
        client.revalidateStartup?.({resetScope:true});throw Object.assign(Error(translate(client.uiLocale?.()||'en','identityChanged')),{uiKey:'identityChanged'});
      }
      client.spaces=spaces;activeControls=new Map(spaces.map(space=>[space.id,space.control_id]));
    };
    const emit=(catalog,rows,partial,loading)=>{
      current();previous=rows;previousGeneration=generation;client.assistants=rows;
      const result={data:{spaces:catalog.items,assistants:rows,activeSpace:catalog.active_space_id||catalog.items[0]?.id,partial,loading},freshness:partial?'stale':loading||rows.some(item=>item.retained)?'retained':'fresh'};
      publish(result);settleReaders(result.data);return result;
    };
    try{
    // Hydrate the whole available display before starting any transport. A miss
    // in one dependency must not hide a committed directory in another Space.
    if(!directory.snapshot.data&&resources.query){
      const saved=await resources.query('/api/v1/spaces',{owner:'',space:''});current();
      if(saved?.value?.items?.length){
        const catalog=saved.value;adoptSpaces(catalog.items);
        const rows=await Promise.all(catalog.items.map(async space=>{
          const [own,links]=await Promise.all([resources.query('/api/v1/assistants',{space:space.id,owner:''}),resources.query('/api/v1/agent-links',{space:space.id,owner:''})]);current();
          return [...local(own?.value||space.assistants||[],space,space.connected===false||client.onlineReady===false),...projectLinkedAssistants(links?.value,space)].map(item=>({...retained(item),offline:item.offline||client.onlineReady===false}));
        }));
        emit(catalog,rows.flat(),false,true);
      }
    }
    const catalog=await resources.read('/api/v1/spaces',{owner:'',space:'',authoritative:force}),spaces=catalog.items||[];
    adoptSpaces(spaces);
    if(!spaces.length)throw Error('No serving Space');
    let partial=false;const confirmedSpaces=new Set();
    const parts=spaces.map(space=>{const rows=previous.filter(item=>item.space_id===space.id&&item.control_id===(space.control_id||space.id)).map(retained);return {space,own:rows.filter(item=>!item.origin),linked:rows.filter(item=>item.origin),pending:2};});
    const rows=()=>parts.flatMap(part=>[...part.own,...part.linked]);
    const progress=()=>{if(resources.query)emit(catalog,rows(),partial,parts.some(part=>part.pending));};
    const recover=(error,part,key)=>{
      if(error.storage||error.name==='AbortError')throw error;
      current();partial=true;part[key]=part[key].map(item=>({...retained(item),offline:true}));
    };
    const dependencies=[];
    const dependency=(path,part,load)=>{
      let pending,settled=false;
      const refresh=force=>{
        if(pending)return pending;
        if(settled){settled=false;part.pending++;const key=path==='/api/v1/assistants'?'own':'linked';part[key]=part[key].map(retained);progress();}
        pending=load(force).then(()=>{current();if(!settled){settled=true;part.pending--;}progress();}).finally(()=>pending=null);
        return pending;
      };
      dependencies.push({path,space:part.space.id,refresh});return refresh(force);
    };
    const displayReads=parts.flatMap(part=>{
      const {space}=part;
      return [
        dependency('/api/v1/assistants',part,async readForce=>{try{
          confirmedSpaces.delete(space.id);
          const items=await resources.read('/api/v1/assistants',{space:space.id,owner:'',authoritative:readForce});current();
          part.own=local(items,space,space.connected===false||client.onlineReady===false);
          if(force===true&&space.connected!==false&&(!resources.state(catalog)||resources.state(catalog).freshness==='fresh')&&(!resources.state(items)||resources.state(items).freshness==='fresh'))confirmedSpaces.add(space.id);
        }catch(error){recover(error,part,'own');}}),
        dependency('/api/v1/agent-links',part,async readForce=>{try{
          const links=await resources.read('/api/v1/agent-links',{space:space.id,owner:'',authoritative:readForce});current();
          const fresh=!resources.state(links)||resources.state(links).freshness==='fresh';
          const linked=projectLinkedAssistants(links,space).map(item=>client.onlineReady===false?{...retained(item),offline:true}:fresh?item:retained(item));
          for(const item of part.linked.filter(item=>!linked.some(value=>value.id===item.id))){if(links.items?.some(link=>link.id===item.origin.link_id&&link.status!=='connected'))linked.push({...retained(item),offline:true});}
          part.linked=linked;
        }catch(error){recover(error,part,'linked');}}),
      ];
    });
    // A slow link or Space cannot hold event-driven updates to already loaded
    // siblings behind the aggregate query flight. Each dependency owns one IO;
    // resource-cache epochs fence invalidations that arrive during that IO.
    if(resources.query){
      const refreshDependencies=(match,readForce=false)=>{try{current();}catch{return;}for(const dependency of dependencies.filter(match))dependency.refresh(readForce).catch(error=>{if(error.name==='AbortError')return;try{current();}catch{return;}if(error.storage){revoke();backgroundError=error;}directory.fail(error);});};
      refreshPending=refreshThis=()=>refreshDependencies(()=>true,true);
      stopProgress.push(resources.subscribe((meta,error)=>{if(!error)refreshDependencies(dependency=>(!meta.space||dependency.space===meta.space)&&dependency.path===meta.path?.split('?')[0]);}));
      stopProgress.push(resources.subscribeInvalidation((topics,source={})=>{if(source.reset||topics.includes('spaces'))return;refreshDependencies(dependency=>(!source.space||dependency.space===source.space)&&(!topics.length||(dependency.path==='/api/v1/agent-links'?topics.includes('agent-links'):topics.some(topic=>['assistants','conversation','assistant-mail','permissions','tasks'].includes(topic)))),'invalidated');}));
      for(const stop of stopProgress)activeStops.add(stop);
    }
    for(const {space}of parts){
      const key=JSON.stringify([generation,serving,space.id,space.control_id]);
      if(metadataLoads.has(key))continue;
      const presentation=machinePresentation(client),before=presentation.revision;
      const pending=presentation.load({space:space.id}).then(()=>{identityCurrent();if(presentation.revision!==before&&directory.active())directory.requery();}).catch(error=>{
        if(error.name==='AbortError')return;
        try{identityCurrent();}catch{return;}
        if(error.storage){if(authorityRevision===(client.directoryAuthorityRevision||0)){revoke();backgroundError=error;}directory.fail(error);}
      }).finally(()=>{if(metadataLoads.get(key)===pending)metadataLoads.delete(key);});
      metadataLoads.set(key,pending);
    }
    await Promise.all(displayReads).then(()=>{
        current();
        // Publish one authority revision after all assistant reads complete:
        // changing it aborts outstanding directory reads in the adapter.
        // Optional machine labels cannot delay this qualification.
        if(force===true&&client.onlineReady!==false&&confirmedSpaces.size){client.directorySpaces=confirmedSpaces;client.directoryGeneration=generation;client.directoryAuthorityRevision=(client.directoryAuthorityRevision||0)+1;authorityRevision=client.directoryAuthorityRevision;}
        progress();
      });
    current();previous=rows();previousGeneration=generation;client.assistants=previous;
    // Only this connection's explicit network refresh can grant authority.
    // Failed Spaces and retained linked assistants cannot borrow another scope's
    // confirmation; an unavailable peer does not block confirmed local work.
    return emit(catalog,previous,partial,parts.some(part=>part.pending));
    }catch(error){
      failed=true;settleReaders(undefined,error);
      // Revoke the shared command authority, not the committed display or local
      // outbox admission. A retired read cannot revoke a later confirmation.
      if(error.storage)revoke();
      throw error;
    }finally{for(const stop of stopProgress){activeStops.delete(stop);stop();}if(refreshPending===refreshThis)refreshPending=null;}
  },{invalidationForce:false,publishIntermediate:true,paths:['/api/v1/spaces','/api/v1/assistants','/api/v1/agent-links','/api/v1/runtime'],topics:['spaces','assistants','assistant-mail','agent-links','conversation','runtime','permissions','tasks','machines']});
  const firstValue=()=>directory.snapshot.data?Promise.resolve(directory.snapshot.data):new Promise((resolve,reject)=>{const reader={resolve,reject};firstReaders.add(reader);directory.refresh().then(value=>{if(firstReaders.delete(reader))resolve(value.data);},error=>{if(firstReaders.delete(reader))reject(error);});});
  return {watch:listener=>directory.watch(listener),query:firstValue,refresh(){refreshPending?.();return directory.refresh(true);},dispose(){closed=true;refreshPending=null;for(const stop of activeStops)stop();activeStops.clear();metadataLoads.clear();settleReaders(undefined,new DOMException('Directory disposed','AbortError'));},
    async confirm(item){const space=client.spaces.find(space=>space.id===item.space_id);if(!space||space.connected===false)return {...item,offline:true};const value=await resources.read(item.origin?'/api/v1/agent-links':'/api/v1/assistants',{space:space.id,owner:'',authoritative:true});const items=item.origin?projectLinkedAssistants(value,space):local(value,space,false);return items.find(current=>current.id===item.id)||(item.origin&&value.items?.some(link=>link.id===item.origin.link_id&&link.status!=='connected')?{...item,offline:true}:null);},
    create:(scope,{profile,model,...values},requestID)=>commands.run(scope,'assistant:create',{...values,selection:{profile_id:profile.id,profile_revision:profile.revision,model}},(body,id,captured)=>client.request('/api/v1/assistants',{...captured,method:'POST',body:{...body,request_id:id}}),{requestID,idempotent:true}),
    update:(scope,id,values)=>commands.run(scope,'assistant:update:'+id,values,(body,_,captured)=>client.request('/api/v1/assistants/'+encodeURIComponent(id),{...captured,requiredCapabilities:['assistant.configure','assistant.profile'],method:'POST',body})),
    remove:(scope,id,revision)=>commands.run(scope,'assistant:remove:'+id,{revision},(body,_,captured)=>client.request('/api/v1/assistants/'+encodeURIComponent(id)+'/delete',{...captured,requiredCapabilities:['assistant.delete'],method:'POST',body})),
    profile:(scope,signal)=>client.request('/api/v1/assistants/profile',{...scope,signal}),
    defaultAvatar:scope=>client.request('/api/v1/assistants/default-avatar',scope),
    cachedConfiguration:scope=>resources.query('/api/v1/agent',scope),
    configuration:(scope,signal)=>resources.read('/api/v1/agent',{...scope,signal,authoritative:true}),
    cachedInstructions:scope=>resources.query('/api/v1/instructions',scope),
    instructions:(scope,signal)=>resources.read('/api/v1/instructions',{...scope,signal,authoritative:true}),
    configure:(scope,input)=>commands.run(scope,'assistant:configure',input,(body,_,captured)=>client.request('/api/v1/agent',{...captured,requiredCapabilities:['assistant.configure'],method:'POST',body})),
    saveInstructions:(scope,input)=>commands.run(scope,'assistant:instructions',input,(body,_,captured)=>client.request('/api/v1/instructions',{...captured,requiredCapabilities:['assistant.configure'],method:'POST',body})),
  };
}
