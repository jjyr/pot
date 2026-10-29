import {sourceServer,launchBrowser,isolatedPage} from './browser-harness.mjs';
const source=await sourceServer(),browser=await launchBrowser();
try{
 await isolatedPage(browser,source.origin,async page=>{
  await page.goto(source.origin+'/mcp-connect.html');
  const results=await page.evaluate(async()=>{
   const {createIndexedStore,idbRequest}=await import('/web/src/state/indexed-store.mjs');
   const {migrateResourceBytes}=await import('/pkg/storage/migrations/ui-cache/0008_resource_bytes.mjs');
   const {migrateStableProjections}=await import('/pkg/storage/migrations/ui-cache/0009_stable_projections.mjs');
   const {migrateMCPConnections}=await import('/pkg/storage/migrations/ui-cache/0010_mcp_connections.mjs');
   const {migrateTaskParameters,UI_CACHE_VERSION}=await import('/pkg/storage/migrations/ui-cache/0011_task_parameters.mjs');
   const {displayProjection}=await import('/web/src/state/resource-cache.mjs');
   const results=[],check=(ok,label)=>{if(!ok)throw Error(label);results.push(label);};
   for(const version of [8,9,10]){
    const name='task-parameters-upgrade-'+version+'-'+crypto.randomUUID(),before=createIndexedStore({name,version,migrate:version===8?migrateResourceBytes:version===9?migrateStableProjections:migrateMCPConnections});
    const meta={control:'control',space:'local',owner:'',boot:'old-boot',topics:['agent-links']};
    const row=(path,value)=>{const m={...meta,path};return {key:JSON.stringify({...m,...(version>=9&&!path.startsWith('@command:')?{boot:undefined}:{})}),meta:m,value,bytes:10,expires:Date.now()+60000,lastConfirmed:1};};
    const rows=[row('/api/v1/agent',[{id:'old',authorization_url:'private'}]),row('@command:/api/v1/agent',{state:'private'}),row('/api/v1/assistants',[{id:'keep'}])];
    await before.transaction(['projections','cacheStats','composerDrafts'],'readwrite',tx=>{for(const row of rows)tx.objectStore('projections').put(row);tx.objectStore('cacheStats').put({key:'projections',bytes:30,count:3});tx.objectStore('composerDrafts').put({key:'draft',text:'unsent'});});before.close();
    const aborted=createIndexedStore({name,version:UI_CACHE_VERSION,migrate(db,tx,old){migrateTaskParameters(db,tx,old);tx.abort();}});let failed=false;try{await aborted.transaction(['projections'],'readonly',()=>{});}catch{failed=true;}aborted.close();
    const old=createIndexedStore({name,version,migrate:version===8?migrateResourceBytes:version===9?migrateStableProjections:migrateMCPConnections});check(failed&&(await old.transaction(['projections'],'readonly',tx=>idbRequest(tx.objectStore('projections').count())))===3,'v'+version+' aborted migration preserves prior records');old.close();
    const upgraded=createIndexedStore({name});
    const [saved,stats,draft]=await upgraded.transaction(['projections','cacheStats','composerDrafts'],'readonly',tx=>Promise.all([idbRequest(tx.objectStore('projections').getAll()),idbRequest(tx.objectStore('cacheStats').get('projections')),idbRequest(tx.objectStore('composerDrafts').get('draft'))]));
    check(saved.length===3&&saved.filter(row=>row.meta.path!=='/api/v1/assistants').every(row=>row.expires===0)&&saved.find(row=>row.meta.path==='/api/v1/assistants').expires>Date.now(),'v'+version+' expires only assistant projections and receipts');
    check(stats.bytes===30&&stats.count===3&&draft.text==='unsent','v'+version+' retains drafts and correct cache accounting');upgraded.close();
    await new Promise((done,reject)=>{const remove=indexedDB.deleteDatabase(name);remove.onsuccess=done;remove.onerror=()=>reject(remove.error);});
   }
   const projected=displayProjection('/api/v1/agent',{selection:{profile_id:'main',model:'main',task_default:{profile_id:'task',model:'task',context_window:64000,max_output_tokens:4000,reasoning_effort:'high'}}});
   check(projected.selection.task_default.context_window===64000&&projected.selection.task_default.max_output_tokens===4000&&projected.selection.task_default.reasoning_effort==='high','Task parameters survive display projection');
   return results;
  });console.log(results.join('\n'));
 });
}finally{await browser.close();await source.close();}
