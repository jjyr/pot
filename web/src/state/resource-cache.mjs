import {translate} from '../locales/messages.mjs';
import {createProjectionStore} from './projection-store.mjs';
// Memory-only resource reads. Cancellation belongs to a consumer, never shared IO.
export function createResourceCache({now=Date.now,limit=256,onChange=()=>{}}={}) {
  const entries=new Map();
  const copy=value=>value===undefined?undefined:structuredClone(value);
  function entry(key) {
    let value=entries.get(key);
    if(!value)value={key,version:0,at:-Infinity,has:false};
    entries.delete(key);entries.set(key,value);
    for(const [id,item]of entries) {
      if(entries.size<=limit)break;
      if(!item.pending&&id!==key)entries.delete(id);
    }
    return value;
  }
  function refresh(entry,load,notify=true) {
    if(entry.pending)return entry.pending;
    // First-load consumers already await this result. Notifying them again can
    // restart a list load just as the user selects its next page.
    const background=entry.has&&notify;
    entry.pending=(async()=>{
      for(;;) {
        const version=entry.version;
        try {
          const value=await load();
          if(entries.get(entry.key)!==entry)throw new DOMException('Resource reset','AbortError');
          // An event during IO requires one more read before publishing.
          if(version!==entry.version)continue;
          entry.value=copy(value);entry.has=true;entry.error=null;entry.at=now();entry.confirmedAt=entry.at;entry.source='committed';
          return copy(value);
        }catch(error) {
          if(entries.get(entry.key)===entry&&version!==entry.version)continue;
          if(entries.get(entry.key)===entry){entry.error=error;entry.at=now();}
          throw error;
        }
      }
    })().finally(()=>{
      entry.pending=null;
      if(background&&entries.get(entry.key)===entry)onChange(entry.key,entry.error);
    });
    return entry.pending;
  }
  return {
    read(key,load,{ttl=30000,signal,authoritative=false,notify=true}={}) {
      if(signal?.aborted)return Promise.reject(signal.reason);
      const value=entry(key),fresh=now()-value.at<ttl;
      let pending;
      if(!fresh||authoritative)pending=refresh(value,load,notify);
      if(value.has&&!authoritative) {
        pending?.catch(()=>{});
        // Returning to a failed resource retains its notice, in the current locale.
        if(value.error&&!pending)queueMicrotask(()=>{if(entries.get(key)===value)onChange(key,value.error);});
        return Promise.resolve(copy(value.value));
      }
      pending||=value.pending||refresh(value,load,notify);
      if(!signal)return pending.then(copy);
      return new Promise((resolve,reject)=>{
        const abort=()=>reject(signal.reason);
        signal.addEventListener('abort',abort,{once:true});
        pending.then(result=>{if(!signal.aborted)resolve(copy(result));},reject)
          .finally(()=>signal.removeEventListener('abort',abort));
      });
    },
    peek(key){const value=entries.get(key);return value?.has?copy(value.value):undefined;},
    status(key){const value=entries.get(key);return {has:!!value?.has,lastConfirmed:value?.confirmedAt||value?.at||null,at:value?.at??-Infinity,source:value?.source,error:value?.error||null};},
    set(key,value,{at=now(),notify=true,source='committed',confirmedAt=at}={}){const target=entry(key);target.version++;target.value=copy(value);target.has=true;target.error=null;target.at=at;target.source=source;target.confirmedAt=confirmedAt;if(notify)onChange(key);},
    invalidate(matches=()=>true){for(const [key,value]of entries)if(matches(key)){value.version++;value.at=-Infinity;}},
    reset(matches=()=>true){for(const key of entries.keys())if(matches(key))entries.delete(key);},
  };
}

// Explicit allowlist. Editor versions, credentials, approvals and snapshot streams
// bypass this cache. Agent Link invitations are command-only and never cached.
const resources={
  '@permissions-policy':['permissions','machines'],
  '/api/v1/spaces':['spaces'],
  '/api/v1/assistants':['assistants','conversation','assistant-mail','permissions','tasks','runner'],
  '/api/v1/providers':['providers','extensions'],
  '/api/v1/agent/routing':['settings','providers'],
  '/api/v1/usage/providers':['usage','providers'],
  '/api/v1/integrations':['integrations'],
  '/api/v1/runtime':['runtime','machines'],
  '/api/v1/agent-links':['agent-links'],
  '/api/v1/about':['runtime'],
  '/api/v1/completions':['machines','skills','assistants','agent-links','settings','permissions'],
};
export function createResourceReads(client,{storage=client.production?createProjectionStore({name:client.chatCacheName||'pot-chat-cache'}):null}={}) {
  let pendingStorage=Promise.resolve(),closed=false;
  const hydrated=new Set(),registered=new Set(),queryStates=new WeakMap(),pendingInvalidations=[];
  function flushInvalidations(){
    if(!storage||!pendingInvalidations.length)return pendingStorage;
    pendingStorage=pendingStorage.catch(()=>{}).then(async()=>{while(pendingInvalidations.length){const [topics,source]=pendingInvalidations[0];await storage.invalidate(topics,source);pendingInvalidations.shift();}});
    return pendingStorage;
  }
  const localize=error=>error?.storage?Object.assign(new Error(translate(client.uiLocale?.()||'en','storage.failed'),{cause:error}),error,{name:error.name,diagnostic:error.diagnostic||error.message}):error;
  const listeners=new Set(),invalidations=new Set(),boots=new Map();
  const cache=createResourceCache({onChange:(key,error)=>{
    for(const listener of listeners)listener(JSON.parse(key),localize(error));
  }});
  const describe=(path,options={})=>{
    const scope={...client.scope(),...options},space=(client.spaces||[]).find(item=>item.id===scope.space);
    const base=path.split('?')[0];
    const topics=resources[base]||(/^\/api\/v1\/usage\/(summary|models|requests|tools|compactions|providers)$/.test(base)?['usage']:
      ['/api/v1/tasks/query','/api/v1/tasks/catalog'].includes(base)?['tasks','runner']:base==='/api/v1/schedules'?['schedules','tasks']:/^\/api\/v1\/tasks\/[^/]+(?:\/messages)?$/.test(base)?['tasks','runner']:null);
    return {path,space:scope.space||'',owner:base==='/api/v1/providers'?'':scope.owner||'',
      control:space?.control_id||client.runtime.runtime_id||client.runtime.id,
      boot:boots.get(scope.space)||boots.get('')||client.runtime.boot_id||'',topics};
  };
  const cachedRead=(meta,options,load)=>readCommitted(meta,options,load).catch(error=>{throw localize(error);});
  const readCommitted=async(meta,options,load)=>{
    const ttl=meta.topics.some(topic=>['usage','tasks','schedules','conversation','machines','skills'].includes(topic))?5000:30000;
    // Selection candidates must wait for the current directory. Historical
    // identity display may retain names, but must never supply active targets.
    const mentions=meta.path.startsWith('/api/v1/completions?')&&new URLSearchParams(meta.path.split('?')[1]).get('q')?.startsWith('@');
    const key=JSON.stringify(meta);
    if(storage){
      await flushInvalidations();
      const namespace=JSON.stringify([meta.control,meta.space]);
      if(!registered.has(namespace)){await storage.register(meta);registered.add(namespace);}
      if(!hydrated.has(key)){
        const saved=await storage.read(key);hydrated.add(key);
        if(saved)cache.set(key,saved.value,{at:-Infinity,confirmedAt:saved.lastConfirmed,source:'retained',notify:false});
      }
    }
    const committedLoad=!storage?load:async()=>{
      for(;;){
        const expected=await storage.token(meta),value=await load();
        if(closed)throw new DOMException('Resource disposed','AbortError');
        if(!await storage.commit(key,meta,displayProjection(meta.path,value),expected))continue;
        const saved=await storage.read(key);
        if(!saved?.fresh)continue;
        return saved.value;
      }
    };
    const before=cache.status(key),authoritative=options.authoritative||mentions;
    const value=await cache.read(key,committedLoad,{ttl,signal:options.signal,authoritative,notify:options.notify});
    const status=before.has&&!authoritative?before:cache.status(key);
    if(value&&typeof value==='object')queryStates.set(value,{lastConfirmed:status.lastConfirmed,freshness:status.source==='retained'?'retained':Date.now()-status.at<ttl&&!status.error?'fresh':'stale'});
    return value;
  };
  const stopStorage=storage?.subscribe((event,{remote})=>{
    if(!remote||closed)return;
    if(event.kind==='commit'||event.kind==='remove'){
      hydrated.delete(event.key);cache.reset(key=>key===event.key);
      if(event.meta)for(const listener of listeners)listener(event.meta);
    }else if(event.kind==='invalidate')result.invalidate(event.topics,{...event.source,remote:true});
  });
  const result={
    describe,storage,state:value=>queryStates.get(value),
    policySummary(options={}){return result.collection('@permissions-policy',{...options,authoritative:true,notify:false},async()=>{const value=await (client.transport||client.request)('/api/v1/permissions',{...options,transient:true});return {policy:value.policy};});},
    async query(path,options={}){const meta={...describe(path,options),...(options.collection?{collection:true}:{})},key=JSON.stringify(meta);if(!storage){const value=cache.peek(key);return value===undefined?null:{value,...cache.status(key)};}await flushInvalidations();const saved=await storage.read(key);return saved?{value:saved.value,lastConfirmed:saved.lastConfirmed,freshness:saved.fresh?'retained':'stale'}:null;},
    async applyCommand(path,options,value,topics){
      if(!storage){result.invalidate(topics,options);return value;}
      const meta={...describe(path,options),topics,path:'@command:'+path};
      const id=options.commandID||options.body?.request_id||crypto.randomUUID(),key=JSON.stringify({...meta,id});
      await flushInvalidations();
      const saved=await storage.command(key,meta,displayProjection(path,value));
      result.invalidate(topics,{...options,remote:true});return saved;
    },
    status(path,options={}){const value=cache.status(JSON.stringify(describe(path,options)));return {...value,freshness:value.has?(Date.now()-value.lastConfirmed<5000?'fresh':'stale'):'unknown'};},
    // A complete merged source collection is one entry, even when it required
    // more backend pages than the ordinary resource LRU can retain individually.
    collection(path,options,load){const meta=describe(path,options);return meta.topics?cachedRead({...meta,collection:true},options,load):load();},
    read(path,options={}) {
      const meta=describe(path,options);
      if(options.transient||!meta.topics||options.method&&options.method!=='GET')return (client.transport||client.request)(path,options);
      return cachedRead(meta,options,()=>(client.transport||client.request)(path,{...options,space:meta.space,owner:meta.owner,signal:undefined}));
    },
    subscribe(listener){listeners.add(listener);return ()=>listeners.delete(listener);},
    subscribeInvalidation(listener){invalidations.add(listener);return ()=>invalidations.delete(listener);},
    invalidate(topics=[],source={}) {
      source={space:source.space||'',control:source.control,boot:source.boot,reset:!!source.reset,remote:!!source.remote};
      const matches=key=>{
        const meta=JSON.parse(key);
        return (!source.space||meta.space===source.space||meta.path==='/api/v1/spaces')&&
          (!topics.length||topics.some(topic=>meta.topics.includes(topic))||topics.includes('spaces'));
      };
      if(storage&&!source.remote){
        pendingInvalidations.push([topics,source]);
        flushInvalidations().catch(error=>{for(const listener of listeners)listener({path:'',topics,space:source.space||''},localize(error));});
      }
      const bootChanged=source.boot&&source.boot!==(boots.get(source.space)||boots.get('')||client.runtime.boot_id||'');
      if(source.boot)boots.set(source.space,source.boot);
      if(bootChanged) {
        // Boot identity changes invalidate every resource in that scope,
        // independently of which topic happened to carry the first new version.
        hydrated.clear();cache.reset(key=>{const meta=JSON.parse(key);return !source.space||meta.space===source.space||meta.path==='/api/v1/spaces';});
      }else if(topics.includes('spaces')){hydrated.clear();cache.reset(matches);}
      else {
        // Plugin lifecycle changes alter which connection types may execute.
        // Do not serve the previous enabled/type projection while revalidating.
        if(topics.includes('extensions'))cache.reset(key=>matches(key)&&JSON.parse(key).path==='/api/v1/providers');
        cache.invalidate(matches);
      }
      // Notify after fencing pending reads, before any consumer can select an
      // item from an obsolete projection. This is distinct from read completion.
      return Promise.allSettled([...invalidations].map(listener=>listener(topics,bootChanged?{...source,reset:true}:source)));
    },
    clear(){closed=true;stopStorage?.();storage?.close();cache.reset();boots.clear();hydrated.clear();},
  };
  return result;
}

export function displayProjection(path,value){
  const base=path.split('?')[0],pick=(value,keys)=>Object.fromEntries(keys.filter(key=>value?.[key]!==undefined).map(key=>[key,value[key]]));
  const redact=value=>{if(Array.isArray(value))return value.map(redact);if(!value||typeof value!=='object')return value;const result={};for(const [key,item]of Object.entries(value)){if(key==='default_credential'){result[key]=!!item;continue;}if(/^(api_key|access_token|refresh_token|client_secret|secret|secrets|credential|credentials|authorization|authorization_url|user_code|invitation|private_key|password|token|owner_key)$/i.test(key))continue;result[key]=redact(item);}return result;};
  if(/^\/api\/v1\/tasks\/[^/]+$/.test(base)){
    const result=value?.result;let truncated=!!value?.cache_truncated;
    const text=value=>{if(typeof value==='string'&&value.length>1024*1024){truncated=true;return value.slice(-1024*1024);}return value;};
    const error=text(value?.error),resultText=text(result?.text),resultError=text(result?.outcome?.error);
    return {...value,error,...(result?{result:{...result,text:resultText,...(result.outcome?{outcome:{...result.outcome,error:resultError}}:{})}}:{}),...(truncated?{cache_truncated:true}:{})};
  }
  if(/^\/api\/v1\/tasks\/[^/]+\/messages$/.test(base)){
    const items=Array.isArray(value)?value:value?.items||[],selected=[];let bytes=0,truncated=false;
    for(const input of items.slice().reverse()){
      const text=typeof input.text==='string'?input.text.slice(-1024*1024):input.text,row={...input,text},size=new TextEncoder().encode(JSON.stringify(row)).length;
      if(selected.length===256||bytes+size>16*1024*1024){truncated=true;break;}
      if(text!==input.text)truncated=true;selected.push(row);bytes+=size;
    }
    return {...(Array.isArray(value)?{}:value),items:selected.reverse(),...(truncated?{cache_truncated:true}:{})};
  }
  if(base==='/api/v1/providers'){
    const profile=value=>pick(value,['id','revision','name','provider','model','auth_ref','model_settings','params','tool_policy','models']);
    if(!value?.profiles&&!value?.provider_types)return redact(profile(value));
    return redact({...pick(value,['configured_models','default_provider_id','directory','model_defaults']),
      profiles:(value.profiles||[]).map(profile),
      connections:(value.connections||[]).map(item=>pick(item,['catalog','type_id','plugin_id','capabilities','available','icon','profile_id','provider_id','endpoint','protocol','has_key'])),
      provider_types:(value.provider_types||[]).map(item=>pick(item,['id','plugin_id','name','endpoint','protocol','protocols','directory_provider','default_credential','capabilities','icon','available','identity']))});
  }
  if(base==='/api/v1/integrations')return redact({items:(value?.items||[]).map(item=>pick(item,['id','name','status','login_available','account','login']))});
  if(base.startsWith('/api/v1/agent-links')||/^\/api\/v1\/(agent|instructions|extensions|spaces|usage)(?:\/|$)/.test(base))return redact(value);
  return value;
}
