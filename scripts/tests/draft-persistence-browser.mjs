// Wait for committed contents rather than draft presentation text. These
// local-Control synthetic fixtures allow attachment selection, whose state follows
// the production draft-ready gate, including an empty or offline draft.
export async function waitForDraftSaved(page,{pending=false,timeout=10000}={}){
 // Poll the async transaction inside evaluate, using its completed result on
 // each rendered frame. A loading editor or a retired conversation cannot
 // satisfy this wait by matching an old empty record.
 await page.evaluate(async({pending,timeout})=>{
  const {createIndexedStore,idbRequest}=await import('/state/indexed-store.mjs'),store=createIndexedStore();
  const deadline=performance.now()+timeout;
  try{
   while(performance.now()<deadline){
    const selected=document.querySelector('.assistant-row.is-selected'),input=document.querySelector('#pot-composer');
    if(selected&&input&&document.querySelector('.attachment-plus:not(:disabled)')&&!document.querySelector('.draft-storage-status.inline-error')){
     const [space,owner]=JSON.parse(selected.dataset.viewKey);
     const [rows,chats,startup]=await store.transaction(['composerDrafts','conversations','startup'],'readonly',tx=>Promise.all([idbRequest(tx.objectStore('composerDrafts').getAll()),idbRequest(tx.objectStore('conversations').getAll()),idbRequest(tx.objectStore('startup').get('last'))]));
     const snapshot=chats.find(chat=>chat.projection.runtime_id===startup?.runtime_id&&chat.projection.owner_id===JSON.stringify([space,owner]))?.projection;
     const row=rows.find(row=>row.key===JSON.stringify([startup?.runtime_id,space,owner]));
     const current=snapshot&&(!row||row.state_key===JSON.stringify([snapshot.runtime_id,snapshot.owner_id,snapshot.conversation_id]));
     // Pending sends opt in; editor clearing alone does not prove that the
     // accepted request fence cleanup has reached disk.
     if(current&&input.isConnected&&selected===document.querySelector('.assistant-row.is-selected')&&!!row?.pending===pending&&(row?.text||'')===input.value&&(row?.attachments.length||0)===document.querySelectorAll('.attachment-card').length&&(row?.references.length||0)===document.querySelectorAll('.composer-references .message-reference').length)return;
    }
    await new Promise(requestAnimationFrame);
   }
   throw Error('Draft did not reach the expected committed state');
  }finally{store.close();}
 },{pending,timeout});
}
