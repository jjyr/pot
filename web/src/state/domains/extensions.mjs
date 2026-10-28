import {displayProjection} from '../resource-cache.mjs';

export function normalizeExtensionCatalog(value){
  if(!Array.isArray(value?.items)||!Array.isArray(value?.mcp))throw Error('invalid_extension_catalog');
  const pick=(value,names)=>Object.fromEntries(names.filter(name=>value[name]!==undefined).map(name=>[name,value[name]]));
  const safe=displayProjection('/api/v1/extensions',value);
  return {items:safe.items.map(item=>pick(item,['id','kind','name','description','source','location','version','icon','enabled','revision','origin','tools','requirements','background','model_providers','manual'])),
    mcp:safe.mcp.map(item=>pick(item,['id','name','revision','enabled','transport','command','args','cwd','url','has_credentials','tools','disabled_tools','status','auth','client_id','scope'])),diagnostics:safe.diagnostics||[]};
}

// Persist display-safe catalogues; every mutation separately confirms the live
// catalogue and target revision. Retained rows never grant command authority.
export function createExtensionsDomain(client,{resources,queries}){
  const catalog=input=>{
    const scope={space:input.space||'',owner:input.owner||'',generation:client.connectionGeneration,boot:client.runtime?.boot_id};
    return queries.define('extensions:'+JSON.stringify(scope),async force=>{
      const value=await resources.read('/api/v1/extensions',{...scope,authoritative:force||false});
      return {data:normalizeExtensionCatalog(value),freshness:resources.state(value)?.freshness||'fresh'};
    },{topics:['extensions','spaces'],paths:['/api/v1/extensions'],space:scope.space,owner:scope.owner,maxAge:1500});
  };
  const confirm=async(scope,item=null)=>{
    const value=normalizeExtensionCatalog(await resources.read('/api/v1/extensions',{...scope,authoritative:true}));
    if(item){
      const target=item.kind==='mcp'?value.mcp.find(entry=>entry.id===item.id):value.items.find(entry=>entry.id===item.id&&entry.kind===item.kind);
      if(!target&&!item.allowMissing||target&&target.revision!==item.revision)throw Object.assign(Error('Extension revision changed'),{uiKey:'settings.saveFailed'});
    }
    return value;
  };
  return {catalog,confirm,dispose(){}};
}
