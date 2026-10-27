import assert from 'node:assert/strict';

// Two real tabs, one IndexedDB namespace and BroadcastChannel. No HTTP backend.
export async function queryResourceCommits(browser,{sourceOrigin}){
  const context=await browser.newContext(),name='query-commits-'+crypto.randomUUID();
  try{
    await context.route('**/query-commits',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><title>Query commit contracts</title>'}));
    const reader=await context.newPage(),writer=await context.newPage();
    for(const page of [reader,writer]){
      await page.goto(sourceOrigin+'/query-commits');
      await page.evaluate(async name=>{
        const {createResourceReads}=await import('/web/src/state/resource-cache.mjs');
        const {createProjectionStore}=await import('/web/src/state/projection-store.mjs');
        const {createQueryRegistry}=await import('/web/src/state/domains/query.mjs');
        window.backendKey=name;
        const client={runtime:{id:'synthetic',boot_id:'boot'},scope:()=>({space:'one',owner:'a'}),request:async()=>({revision:Number(localStorage.getItem(name)||0)})};
        window.reads=createResourceReads(client,{storage:createProjectionStore({name})});
        window.queries=createQueryRegistry(reads);window.counts={};window.stops=[];window.values={};window.announcements=0;
        reads.subscribe(()=>announcements++);
        window.watch=(key,paths,topics,scope={})=>{
          const query=queries.define(key,async force=>{
            counts[key]=(counts[key]||0)+1;
            const data=await Promise.all(paths.map(path=>reads.read(path,{...scope,authoritative:force})));
            return {data};
          },{paths:paths.map(path=>path.split('?')[0]),topics,...scope});
          stops.push(query.watch(state=>values[key]=state));return query;
        };
      },name);
    }
    await reader.evaluate(()=>{
      watch('directory',['/api/v1/spaces','/api/v1/assistants','/api/v1/runtime','/api/v1/agent-links'],['tasks','assistants','permissions','spaces','runtime','agent-links']);
      watch('active-a',['/api/v1/tasks/query?scope=active'],['tasks'],{space:'one',owner:'a'});
      watch('detail',['/api/v1/tasks/worker'],['tasks','runner'],{space:'one',owner:'a'});
    });
    const settle=async()=>{
      await reader.waitForFunction(()=>Object.keys(values).length===Object.keys(counts).length&&Object.values(values).every(value=>value.status==='ready')&&Object.keys(counts).every(key=>!queries.define(key).pending));
      // Let already-delivered BroadcastChannel callbacks and background commits drain.
      await reader.waitForTimeout(50);
      return reader.evaluate(()=>structuredClone(counts));
    };
    let before=await settle();
    const commit=async(path,scope={space:'one',owner:'a'})=>{
      const announced=await reader.evaluate(()=>announcements);
      await writer.evaluate(async({path,scope})=>{localStorage.setItem(backendKey,String(Number(localStorage.getItem(backendKey)||0)+1));await reads.read(path,{...scope,authoritative:true});},{path,scope});
      await reader.waitForFunction(previous=>announcements>previous,announced);
      return settle();
    };
    let after=await commit('/api/v1/tasks/worker');
    assert(after.detail>before.detail,'A remote detail commit wakes its consumer');
    assert.equal(after.directory,before.directory,'A shared tasks topic cannot wake directory');
    assert.equal(after['active-a'],before['active-a'],'Detail commit cannot wake active collection');
    await reader.waitForFunction(()=>values.detail.data[0].revision===1);
    before=await settle();
    after=await commit('/api/v1/tasks/worker',{space:'two',owner:'a'});assert.deepEqual(after,before,'Same path in another Space cannot wake scoped consumers');
    after=await commit('/api/v1/tasks/worker',{space:'one',owner:'b'});assert.deepEqual(after,before,'Same path under another owner cannot wake scoped consumers');
    after=await commit('/api/v1/tasks/query?scope=active&offset=50');
    assert(after['active-a']>before['active-a'],'Remote later-page commit reassembles the collection');assert.equal(after.directory,before.directory);
    for(const path of ['/api/v1/spaces','/api/v1/assistants','/api/v1/runtime','/api/v1/agent-links']){
      before=await settle();after=await commit(path,{space:'one',owner:''});assert(after.directory>before.directory,'Remote directory dependency wakes directory: '+path);
    }
    await reader.evaluate(()=>{stops[1]();watch('active-b',['/api/v1/tasks/query?scope=active'],['tasks'],{space:'one',owner:'b'});});
    before=await settle();after=await commit('/api/v1/tasks/query?scope=active&offset=50',{space:'one',owner:'b'});
    assert.equal(after['active-a'],before['active-a'],'Retired selection stays retired');assert(after['active-b']>before['active-b'],'Current owner consumes its own commits');
    before=after;
    for(const topic of ['tasks','permissions','assistants']){
      await reader.evaluate(topic=>reads.invalidate([topic],{space:'one'}),topic);after=await settle();assert(after.directory>before.directory,'Business topic invalidation still wakes directory: '+topic);before=after;
    }
    await reader.evaluate(()=>queries.define('directory').refresh(true));after=await settle();assert(after.directory>before.directory,'Explicit refresh remains independent of commit matching');
    return {passed:true,checks:['remote detail consumer','unrelated shared topics','Space/owner isolation','collection query strings and pages','all four directory paths','current selection','business invalidation','explicit force']};
  }finally{
    for(const page of context.pages())await page.evaluate(()=>{for(const stop of window.stops||[])stop();window.queries?.dispose();window.reads?.clear();}).catch(()=>{});
    await context.close();
  }
}
