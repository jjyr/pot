import {saveAllowanceConfiguration} from '../provider-save.mjs';
export function createProvidersDomain(client,{resources,queries,commands}){
  const normalize=scope=>({space:scope.space,owner:'',...(scope.configuration?{configuration:true,control_id:scope.control_id,configurationBoot:scope.configurationBoot,configurationRuntime:scope.configurationRuntime}:{})});
  const read=(path,options)=>options.configuration?client.request(path,options):resources.read(path,options);
  const catalog=input=>{const scope=normalize(input);return queries.define('providers:'+JSON.stringify(scope),async force=>({data:await read('/api/v1/providers',{...scope,authoritative:force})}),{topics:['providers','extensions'],space:scope.space,maxAge:30000});};
  const settings=input=>{const scope=normalize(input);return queries.define('settings:'+JSON.stringify(scope),async(force,publish)=>{const data={};await Promise.all([['providers','/api/v1/providers'],['policy','/api/v1/agent/routing'],['allowances','/api/v1/usage/providers']].map(async([name,path])=>{data[name]=await read(path,{...scope,authoritative:force});publish?.({data:{...data},freshness:resources.state(data[name])?.freshness||'fresh'});}));const states=Object.values(data).map(value=>resources.state(value));return {data,freshness:states.some(state=>state&&state.freshness!=='fresh')?'retained':'fresh',lastConfirmed:Math.min(...states.map(state=>state?.lastConfirmed||Date.now()))};},{topics:['providers','settings','usage','spaces'],space:scope.space,maxAge:30000});};
  const write=(scope,operation,path,input,requestID)=>commands.run(scope,operation,input,(body,id,captured)=>client.request(path,{...captured,method:'POST',body:{...body,...(id?{request_id:id}:{})}}),{requestID});
  return {catalog,watchCatalog:(scope,listener)=>catalog({...scope,owner:''}).watch(listener),query:(scope,options={})=>catalog({...scope,owner:''}).value(!!options.authoritative),refresh:scope=>catalog(scope).refresh(true),
    watchSettings:(scope,listener)=>settings(scope).watch(listener),refreshSettings:scope=>settings(scope).refresh(true),
    saveProfile(scope,{values,typeID,profile,configuredModels,catalogProvider,defaultOutput,inputModalities},requestID){
      const {model_selection,context_window,prompt_cache_minutes,...connection}=values;
      return write(scope,'provider:save','/api/v1/providers',{provider_type_id:typeID,profile_id:profile?.id||'',expected_revision:profile?.revision||0,models:[...new Set([...configuredModels,values.model].filter(Boolean))],...connection,reasoning_effort:values.reasoning_effort.trim()||null,max_output_tokens:values.max_output_tokens||defaultOutput,model_settings:{prompt_cache_minutes:prompt_cache_minutes||null,catalog_provider:catalogProvider||null,context_window:context_window||null,max_output_tokens:values.max_output_tokens||null,input_modalities:inputModalities}},requestID);
    },
    saveRouting:(scope,profile,policy,values)=>{const next=structuredClone(policy);next.profile_limits||={};next.provider_limits||={};for(const [key,id,value]of [['profile_limits',profile.id,values.profile_limit],['provider_limits',profile.provider,values.provider_limit]]){if(value)next[key][id]=value;else delete next[key][id];}return write(scope,'routing:save','/api/v1/agent/routing',next);},
    saveAllowance:(scope,quota,policy)=>write(scope,'allowance:save','/api/v1/usage/providers',{provider:quota.provider,revision:quota.revision,policy}),
    allowance:(scope,id,signal)=>read('/api/v1/providers/allowances?profile_id='+encodeURIComponent(id),{...scope,signal,authoritative:true}),
    configureAllowance:(scope,profileID,values,revision)=>commands.run(scope,'provider:monitor:'+profileID,{values,revision},(input,_,captured)=>saveAllowanceConfiguration({request:(path,options)=>client.request(path,options),scope:captured,profileID,...input})),
    refreshAllowance:(scope,profileID,signal)=>client.request('/api/v1/providers/allowances',{...scope,signal,method:'POST',body:{profile_id:profileID,action:'refresh'}}),
    async setDefault(scope,profileID){await write(normalize(scope),'provider:default','/api/v1/providers/default',{provider_id:profileID});return catalog(scope).refresh(true);},
    discover:(scope,input)=>client.request('/api/v1/providers/resolve',{...scope,transient:true,method:'POST',body:input}),
    check:(scope,input)=>client.request('/api/v1/providers/check',{...scope,transient:true,method:'POST',body:input}),
    remove:(scope,id,revision)=>commands.run(scope,'provider:remove:'+id,{profile_id:id,expected_revision:revision},(body,_,captured)=>client.request('/api/v1/providers',{...captured,method:'DELETE',body})),
  };
}
