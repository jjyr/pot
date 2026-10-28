import assert from 'node:assert/strict';
// Synthetic database only. The caller owns the one authorized browser and port.
// Defer the real second tab's lifecycle message to exercise durable fencing.
export async function resourceLogoutAcrossTabs(browser,origin){
 const context=await browser.newContext({serviceWorkers:'block'}),name='resource-logout-lag-'+crypto.randomUUID();
 const first=await context.newPage(),second=await context.newPage(),source={host:'test-host',control:'test-control',space:'local',owner:'alice'},results=[];
 try{
  await second.addInitScript(()=>{
   const Original=BroadcastChannel;window.delayedForget=[];
   window.BroadcastChannel=class extends Original{
    set onmessage(listener){super.onmessage=this.name==='pot-ui-cache-lifecycle'?event=>delayedForget.push(()=>listener(event)):listener;}
    get onmessage(){return super.onmessage;}
   };
  });
  for(const page of [first,second])await page.goto(origin+'/scripts/tests/fixtures/prepared-empty.html');
  await first.evaluate(async({name,source})=>{const{createResourceCopies}=await import('/state/resource-bytes.mjs');window.logoutCopy=createResourceCopies({name});window.logoutSource=source;window.logoutName=name;const token=await logoutCopy.token(source);await logoutCopy.commit(source,'bytes','saved',new Blob(['saved']),token.generation);},{name,source});
  const captured=await second.evaluate(async({name,source})=>{const{createResourceCopies}=await import('/state/resource-bytes.mjs');window.logoutCopy=createResourceCopies({name});window.logoutSource=source;window.capturedAuthority=(await logoutCopy.token(source)).generation;return capturedAuthority;},{name,source});
  await first.evaluate(async()=>{const{clearBusinessCaches}=await import('/state/cache-lifecycle.mjs');await clearBusinessCaches({name:logoutName});});
  await second.waitForFunction(()=>delayedForget.length>0);
  const rejected=await second.evaluate(async()=>{try{await logoutCopy.commit(logoutSource,'bytes','late',new Blob(['late']),capturedAuthority);return false;}catch(error){return error.name==='NotAllowedError';}});
  assert(rejected,'The old tab remains open but its pre-logout write must fail durably');results.push('late old-tab write rejected before delayed forget delivery');
  await second.evaluate(()=>{for(const deliver of delayedForget.splice(0))deliver();});
  assert(await second.evaluate(async()=>{try{await logoutCopy.token(logoutSource);return false;}catch(error){return error.message==='resource_cache_closed';}}),'Late lifecycle delivery still retires the old client');results.push('late forget retires old client without changing committed state');
  const fresh=await first.evaluate(async()=>{const{createResourceCopies}=await import('/state/resource-bytes.mjs');window.logoutCopy=createResourceCopies({name:logoutName});const token=await logoutCopy.token(logoutSource);await logoutCopy.commit(logoutSource,'bytes','fresh',new Blob(['fresh']),token.generation);return{generation:token.generation,text:await(await logoutCopy.read(logoutSource,'bytes','fresh')).value.text()};});
  assert(fresh.generation!==captured&&fresh.text==='fresh');results.push('fresh post-logout writer can commit at the new epoch');return results;
 }finally{
  for(const page of [first,second])await page.evaluate(()=>logoutCopy?.close()).catch(()=>{});
  await first.evaluate(async name=>{for(const value of [name,name+'-tasks'])await new Promise((resolve,reject)=>{const operation=indexedDB.deleteDatabase(value);operation.onsuccess=resolve;operation.onerror=()=>reject(operation.error);});},name).catch(()=>{});
  await context.close();
 }
}
