// Shared query ownership: disposing one consumer never cancels shared IO.
export function createQueryRegistry(resources){
  const queries=new Map();let closed=false;
  const state=(data,status='ready',freshness='fresh',error=null)=>({data,status,freshness,error,lastConfirmed:status==='ready'&&freshness==='fresh'?Date.now():0});
  function define(key,load,{topics=[],space='',paths=[],maxAge=5000,invalidationForce=true,subscribe}={}){
    if(queries.has(key))return queries.get(key);
    let snapshot=state(undefined,'idle','unknown'),flight,queued;
    const listeners=new Set();let stopExternal;
    const publish=next=>{if(closed)return;snapshot={...next,fresh:next.status==='ready'&&next.freshness==='fresh'};for(const listener of [...listeners])listener(snapshot);};
    const eligible=()=>{if(snapshot.fresh&&Date.now()-snapshot.lastConfirmed>=maxAge)snapshot={...snapshot,freshness:'stale',fresh:false};return snapshot;};
    const request=force=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});return {force,promise,resolve,reject};};
    const enqueue=force=>{queued ||= request(false);queued.force ||= force;return queued.promise;};
    function run(batch){
      // Install ownership before load/publication can synchronously reenter refresh.
      flight=batch.promise;
      const finish=()=>{flight=null;const next=queued;queued=null;if(next){if(closed)next.resolve(snapshot);else run(next);}};
      (async()=>{const initial=snapshot.data===undefined;if(initial)publish({...snapshot,status:'loading',error:null});
        try{const result=await load(batch.force,result=>{if(initial)publish({...state(result.data),...result,error:null});});const data=result.data,freshness=result.freshness||resources.state(data)?.freshness||'fresh';publish({...state(data,'ready',freshness),...result,lastConfirmed:result.lastConfirmed??resources.state(data)?.lastConfirmed??(freshness==='fresh'?Date.now():snapshot.lastConfirmed),error:null});return snapshot;}
        catch(error){if(error.name!=='AbortError')publish({...snapshot,status:'error',freshness:snapshot.data===undefined?'unknown':'stale',error});throw error;}
      })().then(value=>{finish();batch.resolve(value);},error=>{finish();batch.reject(error);});
      return batch.promise;
    }
    const query={
      get snapshot(){return eligible();},
      refresh(force=false){
        if(closed)return Promise.resolve(snapshot);
        eligible();
        if(flight)return force?enqueue(true):flight;
        return run(request(force));
      },
      requery(){if(closed)return;if(flight)enqueue(false).catch(()=>{});else query.refresh().catch(()=>{});},
      fail(error){publish({...snapshot,status:'error',freshness:snapshot.data===undefined?'unknown':'stale',error});},
      watch(listener){listeners.add(listener);if(listeners.size===1)stopExternal=subscribe?.(()=>query.requery());listener(eligible());query.refresh().catch(()=>{});return()=>{listeners.delete(listener);if(!listeners.size){stopExternal?.();stopExternal=null;}};},
      async value(force=false){return (await query.refresh(force)).data;},
      matches(meta){return (!space||!meta.space||space===meta.space)&&(paths.includes(meta.path)||!meta.topics?.length||meta.topics?.some(topic=>topics.includes(topic)));},
      invalidationForce,active(){return listeners.size>0;},get pending(){return !!flight;},invalidate(){if(snapshot.data!==undefined)publish({...snapshot,freshness:'stale'});},
      dispose(){stopExternal?.();stopExternal=null;listeners.clear();},
    };queries.set(key,query);for(const [oldKey,old]of queries){if(queries.size<=128)break;if(!old.active()&&!old.pending&&old!==query){old.dispose();queries.delete(oldKey);}}return query;
  }
  const notify=(topics,source={})=>Promise.allSettled([...queries.values()].filter(query=>query.active()&&query.matches({...source,topics})).map(query=>{query.invalidate();return query.refresh(query.invalidationForce);}));
  const stop=resources.subscribe((meta,error)=>{for(const query of queries.values())if(query.active()&&query.matches(meta)){if(error)query.fail(error);else query.requery();}});
  const stopInvalidation=resources.subscribeInvalidation(notify);
  return {define,notify,dispose(){closed=true;stop();stopInvalidation();for(const query of queries.values())query.dispose();queries.clear();}};
}
export function createCommands(client){
  const attempts=new Map(),listeners=new Set();
  const emit=(scope,operation,state)=>{for(const listener of listeners)listener({scope,operation,...state});};
  return {
    watch(listener){listeners.add(listener);return()=>listeners.delete(listener);},
    pending(scope,prefix){return [...attempts.values()].some(value=>value.pending&&value.scope.space===scope.space&&value.scope.owner===scope.owner&&value.operation.startsWith(prefix));},
    run(scope,operation,input,send,{requestID,idempotent=false}={}){
      const captured={...scope},key=JSON.stringify([captured.space,captured.owner,operation,input,requestID]);let attempt=attempts.get(key);
      if(attempt?.pending)return attempt.pending;
      if(!attempt){attempt={scope:captured,operation,id:requestID||(idempotent?crypto.randomUUID():undefined),input:structuredClone(input)};attempts.set(key,attempt);}
      attempt.pending=Promise.resolve().then(()=>send(attempt.input,attempt.id,captured)).then(data=>{attempts.delete(key);emit(captured,operation,{status:'success',data,error:null,accepted:true});return data;},error=>{attempt.pending=null;attempt.accepted=!!error.accepted;emit(captured,operation,{status:'error',error,accepted:attempt.accepted});throw error;}).finally(()=>attempt.pending=null);
      emit(captured,operation,{status:'pending',error:null,accepted:!!attempt.accepted});
      return attempt.pending;
    },
    dispose(){listeners.clear();attempts.clear();},
  };
}
