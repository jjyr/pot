// Run asynchronous IDB reads to transaction completion inside evaluate. Browser
// polling APIs treat a returned Promise as truthy and cannot establish storage.
export async function waitForOutbox(page,predicate,{timeout=12000}={}){
 return page.evaluate(async({predicate,timeout})=>{
  const [{createChatOutboxStore},{createIndexedStore,idbRequest}]=await Promise.all([
   import('/state/chat-outbox-store.mjs'),
   import('/state/indexed-store.mjs')
  ]);
  const outbox=createChatOutboxStore(),store=createIndexedStore();
  const check=new Function('value','return ('+predicate+')(value)'),deadline=performance.now()+timeout;
  try{
   while(performance.now()<deadline){
    const [rows,drafts,records]=await Promise.all([
     outbox.listSaved(),
     store.transaction(['composerDrafts'],'readonly',tx=>idbRequest(tx.objectStore('composerDrafts').getAll())),
     store.transaction(['chatRecordFiles'],'readonly',tx=>idbRequest(tx.objectStore('chatRecordFiles').getAll()))
    ]);
    const files=records.map(({blob,...rest})=>({...rest,size:blob.size}));
    const value=[rows,drafts,files];
    if(check(value))return {rows,drafts,files};
    await new Promise(requestAnimationFrame);
   }
   throw Error('Real IDB outbox predicate timed out: '+predicate);
  }finally{
   try{outbox.close();}finally{store.close();}
  }
 },{predicate:String(predicate),timeout});
}
