import {createResourceCache,displayProjection} from '../resource-cache.mjs';

export function normalizeExtensionCatalog(value){
  if(!Array.isArray(value?.items)||!Array.isArray(value?.mcp))throw Error('invalid_extension_catalog');
  const pick=(value,names)=>Object.fromEntries(names.filter(name=>value[name]!==undefined).map(name=>[name,value[name]]));
  const safe=displayProjection('/api/v1/extensions',value);
  return {items:safe.items.map(item=>pick(item,['id','kind','name','description','source','location','version','icon','enabled','revision','origin','tools','requirements','background','model_providers','manual'])),
    mcp:safe.mcp.map(item=>pick(item,['id','name','revision','enabled','transport','command','args','cwd','url','has_credentials','tools','disabled_tools','status','auth','client_id','scope'])),diagnostics:safe.diagnostics||[]};
}

// Catalogues are display-safe but stay in memory: editor versions and OAuth
// session material must not acquire a persistent/offline command contract.
export function createExtensionsDomain(client,{resources,queries}){
  const cache=createResourceCache();
  const stop=resources.subscribeInvalidation((topics,source={})=>{
    if(!topics.length||topics.some(topic=>['extensions','spaces'].includes(topic)))cache.invalidate(key=>!source.space||JSON.parse(key).space===source.space);
  });
  const catalog=input=>{
    const scope={space:input.space||'',owner:input.owner||'',generation:client.connectionGeneration,boot:client.runtime?.boot_id};
    const key=JSON.stringify(scope);
    return queries.define('extensions:'+key,async force=>{
      // All invalidation listeners fence memory before the read starts.
      await Promise.resolve();
      const data=await cache.read(key,async()=>normalizeExtensionCatalog(await client.request('/api/v1/extensions',{space:scope.space,owner:scope.owner,transient:true})),{ttl:1500,authoritative:force||'invalidated',notify:false});
      return {data};
    },{topics:['extensions','spaces'],paths:['/api/v1/extensions'],space:scope.space,owner:scope.owner,maxAge:1500});
  };
  return {catalog,dispose(){stop();cache.reset();}};
}
