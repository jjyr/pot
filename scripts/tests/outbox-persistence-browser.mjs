// Run asynchronous IDB reads to transaction completion inside evaluate. Browser
// polling APIs treat a returned Promise as truthy and cannot establish storage.
export async function waitForOutbox(page,predicate,{timeout=12000}={}){
 return page.evaluate(async({predicate,timeout})=>{
  const {createIndexedStore,idbRequest}=await import('/state/indexed-store.mjs'),store=createIndexedStore();
  const check=new Function('value','return ('+predicate+')(value)'),deadline=performance.now()+timeout;
  try{while(performance.now()<deadline){const value=await store.transaction(['chatOutbox','composerDrafts','chatOutboxFiles'],'readonly',tx=>Promise.all([idbRequest(tx.objectStore('chatOutbox').getAll()),idbRequest(tx.objectStore('composerDrafts').getAll()),idbRequest(tx.objectStore('chatOutboxFiles').getAll())]));if(check(value))return {rows:value[0],drafts:value[1],files:value[2].map(({blob,...rest})=>({...rest,size:blob.size}))};await new Promise(requestAnimationFrame);}throw Error('Real IDB outbox predicate timed out: '+predicate);}finally{store.close();}
 },{predicate:String(predicate),timeout});
}
