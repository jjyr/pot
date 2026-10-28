import {sourceServer,launchBrowser,isolatedPage} from './browser-harness.mjs';
const source=await sourceServer(),browser=await launchBrowser();
try{
 await isolatedPage(browser,source.origin,async page=>{
  await page.goto(source.origin+'/mcp-connect.html');
  const results=await page.evaluate(async()=>{
   const {createIndexedStore,idbRequest}=await import('/web/src/state/indexed-store.mjs');
   const {migrateResourceBytes}=await import('/pkg/storage/migrations/ui-cache/0008_resource_bytes.mjs');
   const {migrateStableProjections}=await import('/pkg/storage/migrations/ui-cache/0009_stable_projections.mjs');
   const {migrateMCPConnections,UI_CACHE_VERSION}=await import('/pkg/storage/migrations/ui-cache/0010_mcp_connections.mjs');
   const {displayProjection}=await import('/web/src/state/resource-cache.mjs');
   const results=[],check=(ok,label)=>{if(!ok)throw Error(label);results.push(label);};
   for(const version of [8,9]){
    const name='mcp-upgrade-'+version+'-'+crypto.randomUUID(),before=createIndexedStore({name,version,migrate:version===8?migrateResourceBytes:migrateStableProjections});
    const meta={control:'control',space:'local',owner:'',boot:'old-boot',topics:['agent-links']};
    const row=(path,value)=>{const m={...meta,path};return {key:JSON.stringify({...m,...(version===9&&!path.startsWith('@command:')?{boot:undefined}:{})}),meta:m,value,bytes:10,expires:Date.now()+60000,lastConfirmed:1};};
    const rows=[row('/api/v1/agent-links/mcp',[{id:'old',authorization_url:'private'}]),row('@command:/api/v1/agent-links/mcp',{state:'private'}),row('/api/v1/assistants',[{id:'keep'}])];
    await before.transaction(['projections','cacheStats','composerDrafts'],'readwrite',tx=>{for(const row of rows)tx.objectStore('projections').put(row);tx.objectStore('cacheStats').put({key:'projections',bytes:30,count:3});tx.objectStore('composerDrafts').put({key:'draft',text:'unsent'});});before.close();
    const aborted=createIndexedStore({name,version:UI_CACHE_VERSION,migrate(db,tx,old){migrateMCPConnections(db,tx,old);tx.abort();}});let failed=false;try{await aborted.transaction(['projections'],'readonly',()=>{});}catch{failed=true;}aborted.close();
    const old=createIndexedStore({name,version,migrate:version===8?migrateResourceBytes:migrateStableProjections});check(failed&&(await old.transaction(['projections'],'readonly',tx=>idbRequest(tx.objectStore('projections').count())))===3,'v'+version+' aborted migration preserves prior records');old.close();
    const upgraded=createIndexedStore({name});
    const [saved,stats,draft]=await upgraded.transaction(['projections','cacheStats','composerDrafts'],'readonly',tx=>Promise.all([idbRequest(tx.objectStore('projections').getAll()),idbRequest(tx.objectStore('cacheStats').get('projections')),idbRequest(tx.objectStore('composerDrafts').get('draft'))]));
    check(saved.length===1&&saved[0].meta.path==='/api/v1/assistants'&&!JSON.stringify(saved).includes('private'),'v'+version+' removes prior MCP response and command receipt only');
    check(stats.bytes===10&&stats.count===1&&draft.text==='unsent','v'+version+' retains drafts and correct cache accounting');upgraded.close();
    await new Promise((done,reject)=>{const remove=indexedDB.deleteDatabase(name);remove.onsuccess=done;remove.onerror=()=>reject(remove.error);});
   }
   const projected=displayProjection('/api/v1/agent-links/mcp',[{id:'safe',url:'https://user:private@example.com/mcp?token=private#private',state:'active',status:'connected',inbound:false,remote_node_id:'peer',directory:{name:'Name',targets:[{capabilities:['private']}]},last_error:'transport failed token=private',authorization_url:'private',oauth_state:'private',code_verifier:'private'}]);
   check(!JSON.stringify(projected).includes('private')&&projected[0].url==='https://example.com/mcp'&&projected[0].last_error==='unavailable'&&!projected[0].directory.targets,'MCP projection persists only public display identity and semantic status');
   return results;
  });console.log(results.join('\n'));
 });
}finally{await browser.close();await source.close();}
