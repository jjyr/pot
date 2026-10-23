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
  function refresh(entry,load) {
    if(entry.pending)return entry.pending;
    // First-load consumers already await this result. Notifying them again can
    // restart a list load just as the user selects its next page.
    const background=entry.has;
    entry.pending=(async()=>{
      for(;;) {
        const version=entry.version;
        try {
          const value=await load();
          if(entries.get(entry.key)!==entry)throw new DOMException('Resource reset','AbortError');
          // An event during IO requires one more read before publishing.
          if(version!==entry.version)continue;
          entry.value=copy(value);entry.has=true;entry.error=null;entry.at=now();
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
    read(key,load,{ttl=30000,signal,authoritative=false}={}) {
      if(signal?.aborted)return Promise.reject(signal.reason);
      const value=entry(key),fresh=now()-value.at<ttl;
      let pending;
      if(!fresh||authoritative)pending=refresh(value,load);
      if(value.has&&!authoritative) {
        pending?.catch(()=>{});
        // Returning to a failed resource retains its notice, in the current locale.
        if(value.error&&!pending)queueMicrotask(()=>{if(entries.get(key)===value)onChange(key,value.error);});
        return Promise.resolve(copy(value.value));
      }
      pending||=value.pending||refresh(value,load);
      if(!signal)return pending.then(copy);
      return new Promise((resolve,reject)=>{
        const abort=()=>reject(signal.reason);
        signal.addEventListener('abort',abort,{once:true});
        pending.then(result=>{if(!signal.aborted)resolve(copy(result));},reject)
          .finally(()=>signal.removeEventListener('abort',abort));
      });
    },
    peek(key){const value=entries.get(key);return value?.has?copy(value.value):undefined;},
    set(key,value){const target=entry(key);target.version++;target.value=copy(value);target.has=true;target.error=null;target.at=now();onChange(key);},
    invalidate(matches=()=>true){for(const [key,value]of entries)if(matches(key)){value.version++;value.at=-Infinity;}},
    reset(matches=()=>true){for(const key of entries.keys())if(matches(key))entries.delete(key);},
  };
}

// Explicit allowlist. Editor versions, credentials, approvals and snapshot streams
// bypass this cache. Agent Link invitations are command-only and never cached.
const resources={
  '/api/v1/spaces':['spaces'],
  '/api/v1/assistants':['assistants','conversation','assistant-mail','permissions','tasks','runner'],
  '/api/v1/providers':['providers','extensions'],
  '/api/v1/agent/routing':['settings','providers'],
  '/api/v1/usage/providers':['usage','providers'],
  '/api/v1/integrations':['integrations'],
  '/api/v1/machines':['machines'],
  '/api/v1/agent-links':['agent-links'],
  '/api/v1/about':['runtime'],
  '/api/v1/completions':['machines','skills','assistants','agent-links','settings','permissions'],
};
export function createResourceReads(client) {
  const listeners=new Set(),invalidations=new Set(),boots=new Map();
  const cache=createResourceCache({onChange:(key,error)=>{
    for(const listener of listeners)listener(JSON.parse(key),error);
  }});
  const describe=(path,options={})=>{
    const scope={...client.scope(),...options},space=(client.spaces||[]).find(item=>item.id===scope.space);
    const base=path.split('?')[0];
    const topics=resources[base]||(base.startsWith('/api/v1/usage/')?['usage']:
      ['/api/v1/tasks/query','/api/v1/tasks/catalog'].includes(base)?['tasks','runner']:base==='/api/v1/schedules'?['schedules','tasks']:null);
    return {path,space:scope.space||'',owner:base==='/api/v1/providers'?'':scope.owner||'',
      control:space?.control_id||client.runtime.runtime_id||client.runtime.id,
      boot:boots.get(scope.space)||boots.get('')||client.runtime.boot_id||'',topics};
  };
  const cachedRead=(meta,options,load)=>{
    const ttl=meta.topics.some(topic=>['usage','tasks','schedules','conversation','machines','skills'].includes(topic))?5000:30000;
    // Selection candidates must wait for the current directory. Historical
    // identity display may retain names, but must never supply active targets.
    const mentions=meta.path.startsWith('/api/v1/completions?')&&new URLSearchParams(meta.path.split('?')[1]).get('q')?.startsWith('@');
    return cache.read(JSON.stringify(meta),load,{ttl,signal:options.signal,authoritative:options.authoritative||mentions});
  };
  return {
    // A complete merged source collection is one entry, even when it required
    // more backend pages than the ordinary resource LRU can retain individually.
    collection(path,options,load){const meta=describe(path,options);return meta.topics?cachedRead({...meta,collection:true},options,load):load();},
    read(path,options={}) {
      const meta=describe(path,options);
      if(!meta.topics||options.method&&options.method!=='GET')return client.request(path,options);
      return cachedRead(meta,options,()=>client.request(path,{...options,space:meta.space,owner:meta.owner,signal:undefined}));
    },
    subscribe(listener){listeners.add(listener);return ()=>listeners.delete(listener);},
    subscribeInvalidation(listener){invalidations.add(listener);return ()=>invalidations.delete(listener);},
    invalidate(topics=[],source={}) {
      const matches=key=>{
        const meta=JSON.parse(key);
        return (!source.space||meta.space===source.space||meta.path==='/api/v1/spaces')&&
          (!topics.length||topics.some(topic=>meta.topics.includes(topic))||topics.includes('spaces'));
      };
      const bootChanged=source.boot&&source.boot!==(boots.get(source.space)||boots.get('')||client.runtime.boot_id||'');
      if(source.boot)boots.set(source.space,source.boot);
      if(bootChanged) {
        // Boot identity changes invalidate every resource in that scope,
        // independently of which topic happened to carry the first new version.
        cache.reset(key=>{const meta=JSON.parse(key);return !source.space||meta.space===source.space||meta.path==='/api/v1/spaces';});
      }else if(topics.includes('spaces'))cache.reset(matches);
      else {
        // Plugin lifecycle changes alter which connection types may execute.
        // Do not serve the previous enabled/type projection while revalidating.
        if(topics.includes('extensions'))cache.reset(key=>matches(key)&&JSON.parse(key).path==='/api/v1/providers');
        cache.invalidate(matches);
      }
      // Notify after fencing pending reads, before any consumer can select an
      // item from an obsolete projection. This is distinct from read completion.
      for(const listener of invalidations)listener(topics,bootChanged?{...source,reset:true}:source);
    },
    clear(){cache.reset();boots.clear();},
  };
}
