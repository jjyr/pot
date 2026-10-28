import {sourceServer,launchBrowser,isolatedPage,fixture} from './browser-harness.mjs';
const source=await sourceServer(),browser=await launchBrowser();
try{
 await isolatedPage(browser,source.origin,async page=>{
  await page.route('**/__stable',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><title>Stable cache fixture</title>'}));
  await page.goto(source.origin+'/__stable');
  const results=await page.evaluate(async()=>{
   const {createIndexedStore,idbRequest}=await import('/web/src/state/indexed-store.mjs');
   const {migrateResourceBytes}=await import('/pkg/storage/migrations/ui-cache/0008_resource_bytes.mjs');
   const {migrateStableProjections}=await import('/pkg/storage/migrations/ui-cache/0009_stable_projections.mjs');
   const {createProjectionStore,projectionKey}=await import('/web/src/state/projection-store.mjs');
   const {createResourceReads,displayProjection}=await import('/web/src/state/resource-cache.mjs');
   const checks=[],check=(ok,label)=>{if(!ok)throw Error(label);checks.push(label);};
   const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve};};
   const name='stable-projections-'+crypto.randomUUID(),path='/api/v1/assistants',scope={space:'s',owner:'a'},runtime={runtime_id:'c',boot_id:'new'};
   const client={production:true,chatCacheName:name,runtime,connectionGeneration:1,scope:()=>scope,spaces:[{id:'s',control_id:'c'}],request:async()=>({items:[{id:'live'}]})};
   const description=createResourceReads({...client,production:false}),meta=description.describe(path);description.clear();
   const old=createIndexedStore({name,version:8,migrate:migrateResourceBytes});
   const row=(boot,time)=>{const provenance={...meta,boot},value={items:[{id:boot}]};return {schema_version:1,key:JSON.stringify(provenance),meta:provenance,namespace:JSON.stringify(['c','s']),value,token:'[0,0,0,0,0,0]',lastConfirmed:time,used:time,expires:Date.now()+60000,bytes:new TextEncoder().encode(JSON.stringify(value)).length};};
   const a=row('old-a',1),b=row('old-b',2),command={...row('old-command',1),key:'receipt',meta:{...meta,path:'@command:'+path},value:{id:'receipt'}};
   await old.transaction(['projections','cacheStats','composerDrafts'],'readwrite',tx=>{for(const value of [a,b,command])tx.objectStore('projections').put(value);tx.objectStore('cacheStats').put({key:'projections',count:3,bytes:a.bytes+b.bytes+command.bytes});tx.objectStore('composerDrafts').put({key:'draft',text:'keep unsent'});});old.close();
   const aborted=createIndexedStore({name,migrate(db,tx,version){migrateStableProjections(db,tx,version);tx.abort();}});
   let failed=false;try{await aborted.transaction(['projections'],'readonly',()=>{});}catch{failed=true;}aborted.close();
   const previous=createIndexedStore({name,version:8,migrate:migrateResourceBytes});
   check(failed&&(await previous.transaction(['projections'],'readonly',tx=>idbRequest(tx.objectStore('projections').count())))===3,'aborted upgrade preserves preceding boot-key rows');previous.close();
   const db=createIndexedStore({name}),storage=createProjectionStore({name});let reads;
   try{
    const [rows,stats,draft]=await db.transaction(['projections','cacheStats','composerDrafts'],'readonly',async tx=>Promise.all([idbRequest(tx.objectStore('projections').getAll()),idbRequest(tx.objectStore('cacheStats').get('projections')),idbRequest(tx.objectStore('composerDrafts').get('draft'))]));
    const saved=rows.find(value=>value.key===projectionKey(meta));
    check(rows.length===2&&saved.value.items[0].id==='old-b'&&saved.schema_version===2&&saved.meta.boot==='old-b','v8 upgrade deduplicates newest display and keeps boot provenance');
    check(rows.some(value=>value.key==='receipt'&&value.schema_version===1)&&draft.text==='keep unsent','upgrade preserves command receipt and unsent draft');
    check(stats.count===2&&stats.bytes===rows.reduce((sum,value)=>sum+value.bytes,0),'upgrade recomputes projection accounting');
    const network=deferred();client.request=async()=>{await network.promise;return {items:[{id:'live'}]};};reads=createResourceReads(client,{storage});
    const retained=await reads.read(path);check(retained.items[0].id==='old-b'&&reads.state(retained).freshness==='retained','new boot immediately displays persisted old-boot projection');
    let confirmed=false;const fresh=reads.read(path,{authoritative:true}).then(value=>{confirmed=true;return value;});await new Promise(done=>setTimeout(done,20));check(!confirmed,'retained display does not satisfy authoritative read');network.resolve();check((await fresh).items[0].id==='live','authoritative read resolves after network and commit');
    const delayed=deferred(),entered=deferred();client.request=async()=>{entered.resolve();await delayed.promise;return {items:[{id:'late'}]};};
    const pending=reads.read(path,{authoritative:true});await entered.promise;runtime.boot_id='newer';delayed.resolve();failed=false;try{await pending;}catch(error){failed=error.name==='AbortError';}check(failed&&(await reads.query(path)).value.items[0].id==='live','old boot HTTP response cannot overwrite stable projection');
    check(await reads.query(path,{owner:'another'})===null,'stable display isolates assistant identity');
    client.spaces[0].control_id='another';check(await reads.query(path)===null,'stable display isolates Control identity');client.spaces[0].control_id='c';

    // Hold a real IDB writer ahead of a resource commit. Retire the serving
    // identity only after the commit transaction was created and queued.
    for(const retire of ['boot','connection']){
     const ready=deferred();let release=false,held=false,committing=false;
     const original=IDBDatabase.prototype.transaction,originalCommit=storage.commit;
     IDBDatabase.prototype.transaction=function(stores,mode,...args){
      if(mode==='readwrite'&&committing&&!held){held=true;const blocker=original.call(this,['projections'],'readwrite');const pump=()=>{const request=blocker.objectStore('projections').get('gate');request.onsuccess=()=>{ready.resolve();if(!release)pump();};};pump();}
      return original.call(this,stores,mode,...args);
     };
     storage.commit=function(...args){committing=true;return originalCommit.apply(this,args);};
     client.request=async()=>({items:[{id:'queued-'+retire}]});
     try{
      const queued=reads.read(path,{authoritative:true});await ready.promise;
      if(retire==='boot')runtime.boot_id+='-next';else{client.connectionGeneration++;reads.resetConnection();}
      release=true;failed=false;try{await queued;}catch(error){failed=error.name==='AbortError';}
      check(failed&&(await reads.query(path)).value.items[0].id==='live','queued '+retire+' retirement aborts stable-key write');
     }finally{release=true;storage.commit=originalCommit;IDBDatabase.prototype.transaction=original;}
    }
    // A stable key can be replaced by a different window before our readback,
    // even before its BroadcastChannel notification is delivered.
    const originalRead=storage.read,originalCommit=storage.commit;let overwrite=false,injected=false,networkReads=0;
    storage.commit=async function(...args){const result=await originalCommit.apply(this,args);if(result&&!injected)overwrite=true;return result;};
    storage.read=async function(key){if(overwrite&&!injected){injected=true;overwrite=false;const foreign={...reads.describe(path),boot:'foreign-boot'};await originalCommit.call(this,key,foreign,{items:[{id:'foreign'}]},await this.token(foreign));}return originalRead.call(this,key);};
    client.request=async()=>{networkReads++;return {items:[{id:'current-boot'}]};};
    try{const value=await reads.read(path,{authoritative:true});check(value.items[0].id==='current-boot'&&reads.state(value).freshness==='fresh'&&networkReads===2,'foreign-boot readback cannot become fresh current authority');}
    finally{storage.read=originalRead;storage.commit=originalCommit;}
    const safe=displayProjection('/api/v1/agent',{revision:3,config:{source_profile_id:'p',model:'m',api_key:'secret'},selection:{model:'m',task_default:{profile_id:'deepseek',model:'flash',token:'private'}},credentials:'private'});
    check(safe.selection.task_default.model==='flash'&&!JSON.stringify(safe).includes('private')&&!JSON.stringify(safe).includes('secret'),'settings display persists selection without credentials');
    const extension=displayProjection('/api/v1/extensions',{items:[{id:'x',session:{private:'oauth'}}],mcp:[{id:'m',auth:{session:'private'},authorization_url:'private',tools:[]}],diagnostics:[]});
    check(!JSON.stringify(extension).includes('private'),'extension display excludes OAuth session material');
   }finally{reads?.clear();storage.close();db.close();await new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});}
   return checks;
  });console.log(results.join('\n'));
  console.log(await fixture(page,source.origin,'projection-store.html','DONE projection store contract'));
 });
}finally{await browser.close();await source.close();}
