import {createIndexedStore,idbRequest} from '../../web/src/state/indexed-store.mjs';
import {createDraftStore,assistantDraftKey} from '../../web/src/state/draft-store.mjs';
import {createChatOutboxStore,outboxTarget} from '../../web/src/state/chat-outbox-store.mjs';
import {createAssistantDrafts} from '../../web/src/state/assistant-drafts.mjs';
import {createConversationState,ownerKey,setDraft,setDraftReferences,draft,draftReferences} from '../../web/src/state/conversation.mjs';
import {clearBusinessCaches} from '../../web/src/state/cache-lifecycle.mjs';
import {migrateChatOutbox} from '../../pkg/storage/migrations/ui-cache/0007_chat_outbox.mjs';
import {UI_CACHE_VERSION} from '../../pkg/storage/migrations/ui-cache/0012_chat_records.mjs';
import {migrateStartupIdentity} from '../../pkg/storage/migrations/ui-cache/0006_startup_identity.mjs';
import {migrateChatCache} from '../../pkg/storage/migrations/ui-cache/0001_chat_cache.mjs';

const remove=name=>new Promise((resolve,reject)=>{const op=indexedDB.deleteDatabase(name);op.onsuccess=resolve;op.onerror=()=>reject(op.error);});
export async function chatOutboxStorage(report=()=>{}){
 let checks=0;const check=(value,label)=>{if(!value)throw Error(label);checks++;report('PASS '+label);};
 for(const version of [1,6]){
  const name='outbox-upgrade-'+crypto.randomUUID(),old=createIndexedStore({name,version,migrate:version===1?migrateChatCache:migrateStartupIdentity});
  await old.transaction(['messages','views',...(version===6?['composerDrafts','composerFiles','startup']:[])],'readwrite',tx=>{
   tx.objectStore('messages').put({key:'old',id:'message',order:'0001',bytes:7,message:{text:'Keep history'}});tx.objectStore('views').put({key:'old',surface:'test',position:{id:'message'}});
   if(version===6){tx.objectStore('composerDrafts').put({key:'draft',text:'Unsent draft',pending:{request:{request_id:'legacy-receipt'}}});tx.objectStore('composerFiles').put({key:'draft',id:'file',blob:new Blob(['synthetic original'])});tx.objectStore('startup').put({key:'last',runtime_id:'host'});}
  });old.close();
  if(version===6){
   const failedUpgrade=createIndexedStore({name,migrate(db,tx,oldVersion){migrateChatOutbox(db,tx,oldVersion);throw Error('Abort new outbox schema');}});let rolledBack=false;
   try{await failedUpgrade.transaction(['chatOutbox'],'readonly',()=>{});}catch(error){rolledBack=error.message==='Abort new outbox schema';}failedUpgrade.close();
   const previous=createIndexedStore({name,version:6,migrate:migrateStartupIdentity});check(rolledBack&&await previous.transaction(['composerDrafts','composerFiles'],'readonly',async tx=>(await idbRequest(tx.objectStore('composerDrafts').get('draft'))).pending.request.request_id==='legacy-receipt'&&(await idbRequest(tx.objectStore('composerFiles').get(['draft','file']))).blob.size===18),'Aborted v7 upgrade rolls back both outbox stores while preserving old receipts and originals');previous.close();
  }
  const current=createIndexedStore({name});
  check(await current.transaction(['messages','views','chatOutbox','composerDrafts','composerFiles','startup'],'readonly',async tx=>{
   return (await idbRequest(tx.objectStore('messages').get(['old','message']))).message.text==='Keep history'&&(await idbRequest(tx.objectStore('views').get(['old','test']))).position.id==='message'&&(await idbRequest(tx.objectStore('chatOutbox').count()))===0&&(version!==6||(await idbRequest(tx.objectStore('composerDrafts').get('draft'))).pending.request.request_id==='legacy-receipt'&&(await idbRequest(tx.objectStore('composerFiles').get(['draft','file']))).blob.size===18&&(await idbRequest(tx.objectStore('startup').get('last'))).runtime_id==='host');
  }),'v'+version+' upgrade preserves history, drafts, originals, pending receipt and startup without enqueuing any draft');current.close();
  const broken=createIndexedStore({name,version:UI_CACHE_VERSION+1,migrate(db,tx,oldVersion){migrateChatOutbox(db,tx,oldVersion);db.createObjectStore('rollback');throw Error('Synthetic upgrade abort');}});let aborted=false;
  try{await broken.transaction(['chatOutbox'],'readonly',()=>{});}catch(error){aborted=error.message==='Synthetic upgrade abort';}broken.close();
  const reopened=createIndexedStore({name});check(aborted&&(await reopened.transaction(['messages'],'readonly',tx=>idbRequest(tx.objectStore('messages').count())))===1,'Upgrade abort preserves previous schema and allows retry');reopened.close();
  const downgrade=createIndexedStore({name,version:6,migrate:migrateStartupIdentity});let refused=false;try{await downgrade.transaction(['messages'],'readonly',()=>{});}catch(error){refused=error.name==='VersionError';}downgrade.close();check(refused,'Older schema refuses a newer database without wiping it');await remove(name);
 }
 const name='outbox-transactions-'+crypto.randomUUID(),client={chatCacheName:name,runtime:{runtime_id:'host'},spaces:[{id:'space',control_id:'control'},{id:'other',control_id:'control'}]},scope={space:'space',owner:'a'},snapshot={runtime_id:'host',owner_id:JSON.stringify(['space','a']),conversation_id:'chat',boot_id:'boot',running:false,turn_id:null,messages:[]};
 const state=createConversationState();state.snapshot=snapshot;const key=ownerKey(snapshot),draftKey=assistantDraftKey(client,scope),drafts=createDraftStore({name}),one=createChatOutboxStore({name}),two=createChatOutboxStore({name}),raw=createIndexedStore({name});
 const value=(id,text='Explicit submission')=>({target:outboxTarget(client,scope,snapshot),request:{request_id:id,conversation_id:'chat',text,attachments:[],references:[]},draftVersion:state.draftVersions.get(key)||0,referenceVersion:state.referenceVersions.get(key)||0});
 let items=[];
 const attachments={items:()=>items,hydrate(){},discard(){}};
 const controller=createAssistantDrafts({client,state,attachments,storage:drafts,changed(){},restored(){},restorePending(){throw Error('No ordinary draft should auto-send');}});state.draftChanged=key=>controller.change(key);
 try{
  controller.activate(snapshot,scope);await controller.flush(key);setDraft(state,'Explicit submission');setDraftReferences(state,[{message_id:'source',quote:'saved quote'}]);items=[{attachment:{id:'synthetic-file',name:'synthetic.pdf',mime_type:'application/pdf',size:9},file:new File(['synthetic'],'synthetic.pdf',{type:'application/pdf'}),phase:'waiting'}];controller.change(key);await controller.flush(key);
  const submitted=value('one');submitted.request.references=draftReferences(state,key);submitted.request.attachments=items.map(item=>item.attachment);const originalAdd=IDBObjectStore.prototype.add;let committed=false;
  IDBObjectStore.prototype.add=function(...args){const result=originalAdd.apply(this,args);if(this.name==='chatOutbox')result.addEventListener('success',()=>{check(!committed,'Request success is not transaction completion');setDraft(state,'New typing during the submission');setDraftReferences(state,[{message_id:'new-source'}]);});return result;};
  try{await controller.enqueue(key,submitted,()=>{committed=true;if(state.draftVersions.get(key)===submitted.draftVersion)setDraft(state,'',key);if(state.referenceVersions.get(key)===submitted.referenceVersion)setDraftReferences(state,[],key);items=[];});}finally{IDBObjectStore.prototype.add=originalAdd;}
  await controller.flush(key);let rows=await one.listSaved(),saved=await drafts.read(draftKey);
  check(committed&&rows.length===1&&rows[0].request.text==='Explicit submission'&&saved.record.text==='New typing during the submission'&&saved.record.references[0].message_id==='new-source','Transaction completion transfers submission and preserves newer text/reference versions');
  check((await one.files(submitted.target.key,'one'))[0].blob.size===9&&saved.files.length===0,'Offline original transfers atomically from composer to queue');
  setDraft(state,'Keep if storage fails');await controller.flush(key);let cleaned=false;const add=IDBObjectStore.prototype.add;
  IDBObjectStore.prototype.add=function(...args){const request=add.apply(this,args);if(this.name==='chatOutbox')request.addEventListener('success',()=>this.transaction.abort());return request;};
  let rejected=false;try{await controller.enqueue(key,value('must-abort'),()=>{cleaned=true;setDraft(state,'',key);});}catch{rejected=true;}finally{IDBObjectStore.prototype.add=add;}
  check(rejected&&!cleaned&&draft(state,key)==='Keep if storage fails'&&(await one.listSaved()).length===1&&(await drafts.read(draftKey)).record.text==='Keep if storage fails','A successful request followed by transaction abort never clears input or publishes a queue item');
  const sequence=rows[0].sequence;
  const claims=await Promise.all([one.claim(sequence,'tab-one',100,30),two.claim(sequence,'tab-two',100,30)]),winner=claims.find(Boolean),token=winner.claim.token;
  check(claims.filter(Boolean).length===1,'Independent connections atomically claim the same FIFO head once');
  check(!await one.cancel(winner)&&!await one.edit(winner,'Must not change'),'Claim wins deterministically over edit/cancel; no false withdrawal');
  const takeover=await two.claim(sequence,'reopened',131,30);check(takeover&&!await one.update(sequence,token,{state:'accepted'}),'Expired claim can be recovered and stale writer cannot settle it');
  await two.update(sequence,'reopened',{state:'queued',claim:null});rows=await one.listSaved();
  const edited=await one.edit(rows[0],'Edited before dispatch');check(edited.sequence===sequence&&edited.revision>rows[0].revision&&edited.request.text==='Edited before dispatch'&&!await two.edit(rows[0],'Stale editor'),'Unstarted edits keep sequence and reject stale versions');
  const wrongIdentities=expected=>[...['key','host','control','space','owner','conversation'].map(key=>[key,{...expected,target:{...expected.target,[key]:'wrong-identity'}}]),['request',{...expected,request:{...expected.request,request_id:'wrong-request'}}]];
  for(const [part,wrong]of wrongIdentities(edited))check(!await two.cancel(wrong)&&!await one.edit(wrong,'Wrong target mutation')&&(await one.files(submitted.target.key,'one')).length===1,'Cancel and edit reject wrong '+part+' without changing the row or original');
  await one.claim(sequence,'retry-scope',200,30);const failedEdit=await one.update(sequence,'retry-scope',{state:'failed',claim:null});
  for(const [part,wrong]of wrongIdentities(failedEdit))check(!await two.retry(wrong)&&(await one.files(submitted.target.key,'one')).length===1,'Retry rejects wrong '+part+' without changing the row or original');
  const retried=await one.retry(failedEdit);check(retried.state==='queued'&&retried.request.request_id===edited.request.request_id,'Matching failed submission can retry without changing its request identity');
  const cancelled=await two.cancel(retried);check(cancelled.state==='cancelled'&&cancelled.request.text==='Edited before dispatch'&&(await one.files(submitted.target.key,'one')).length===0,'Explicit cancellation preserves metadata and releases only its original');
  await drafts.enqueue(draftKey,value('two'),[]);await drafts.enqueue(draftKey,value('three'),[]);rows=await one.listSaved();
  check(!await one.claim(rows[2].sequence,'skip-head',200,30),'Later queue item cannot bypass its unaccepted FIFO head');
  const race=await Promise.all([one.cancel(rows[1]),two.claim(rows[1].sequence,'racing',200,30)]);check(race.filter(Boolean).length===1,'Cancel/dispatch race has one committed winner');
  await clearBusinessCaches({name});check((await one.listSaved()).length===3&&(await one.files(submitted.target.key,'one')).length===0&&(await drafts.read(draftKey)).record.text==='Keep if storage fails','Business cache clearing preserves explicit queue and ordinary draft without recreating cancelled originals');
  check(outboxTarget(client,{space:'other',owner:'a'},{...snapshot,owner_id:JSON.stringify(['other','a'])}).key!==submitted.target.key&&outboxTarget({...client,spaces:[{id:'space',control_id:'other-control'}]},scope,snapshot).key!==submitted.target.key,'Space and Control are separate immutable queue scopes');
  const otherTarget=outboxTarget({...client,spaces:[{id:'space',control_id:'other-control'}]},scope,snapshot),scoped={...value('same-id'),target:otherTarget};
  await drafts.enqueue(draftKey,value('same-id'),[{id:'synthetic-file',blob:new Blob(['synthetic'])}]);
  await drafts.enqueue(draftKey,scoped,[{id:'synthetic-file',blob:new Blob(['different original'])}]);
  check((await one.listSaved()).filter(row=>row.request.request_id==='same-id').length===2&&(await one.files(otherTarget.key,'same-id'))[0].blob.size===18&&(await one.files(submitted.target.key,'same-id'))[0].blob.size===9,'The same request and attachment IDs remain isolated across Controls');
  const pending=(await one.listSaved()).find(row=>row.request.request_id==='same-id'&&row.target.key===otherTarget.key),claimed=await two.claim(pending.sequence,'accepted-cleanup',300,30);
  await two.update(claimed.sequence,'accepted-cleanup',{started:true,state:'accepted',message_id:'synthetic-accepted',projection_pending:true,claim:null});
  check((await one.files(otherTarget.key,'same-id')).length===1,'Business acceptance retains originals before the local projection commits');
  for(const [part,wrong]of wrongIdentities(pending))check(!await one.releaseFiles(wrong)&&(await one.files(otherTarget.key,'same-id')).length===1&&(await one.listSaved()).find(row=>row.sequence===pending.sequence).projection_pending===true,'Accepted cleanup rejects wrong '+part+' and retains original/projection state');
  await one.releaseFiles(pending);check((await one.files(otherTarget.key,'same-id')).length===0&&(await one.files(submitted.target.key,'same-id')).length===1&&(await one.listSaved()).find(row=>row.sequence===pending.sequence).projection_pending===false,'Projection completion atomically releases accepted originals without touching another Control');
  const write=drafts.write;let releaseWrite,writeStarted,holdOnce=true;const held=new Promise(resolve=>releaseWrite=resolve),started=new Promise(resolve=>writeStarted=resolve);
  items=[{attachment:{id:'capture-before-wait',name:'capture.pdf',mime_type:'application/pdf',size:9},file:new File(['synthetic'],'capture.pdf',{type:'application/pdf'}),phase:'waiting'}];setDraft(state,'Capture the submitted original before awaiting a draft write');controller.change(key);
  const captured=value('capture-before-wait',draft(state,key));captured.request.attachments=items.map(item=>item.attachment);
  drafts.write=async(...args)=>{const saving=write.apply(drafts,args);if(holdOnce){holdOnce=false;writeStarted();await held;}return saving;};
  try{const submission=controller.enqueue(key,captured,()=>{if(state.draftVersions.get(key)===captured.draftVersion)setDraft(state,'',key);});await started;items=[];controller.change(key);releaseWrite();await submission;await controller.flush(key);check((await one.files(captured.target.key,captured.request.request_id))[0].blob.size===9,'Removing an attachment while the draft write is pending cannot strip the submitted original');}finally{releaseWrite();drafts.write=write;}
  // A genuine asynchronous negative condition: success must not be inferred
  // from a Promise returned to a browser polling predicate.
  let impossible=false;const deadline=performance.now()+80;
  while(performance.now()<deadline){const list=await one.listSaved();if(list.some(row=>row.target.control==='impossible-control'))impossible=true;await new Promise(requestAnimationFrame);}
  check(!impossible,'Full-identity impossible IDB predicate reaches its negative timeout');
 }finally{controller.close();one.close();two.close();raw.close();await new Promise(resolve=>setTimeout(resolve,0));await remove(name);await remove(name+'-tasks');}
 report('DONE '+checks+' outbox storage checks');return checks;
}
