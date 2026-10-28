import {UI_CACHE_VERSION} from '../../pkg/storage/migrations/ui-cache/0007_chat_outbox.mjs';
import {createIndexedStore,idbRequest} from '../../web/src/state/indexed-store.mjs';
import {createDraftStore,assistantDraftKey} from '../../web/src/state/draft-store.mjs';
import {createAssistantDrafts} from '../../web/src/state/assistant-drafts.mjs';
import {createConversationState,ownerKey,setDraft,setDraftReferences,beginSend,settleSend} from '../../web/src/state/conversation.mjs';
import {clearBusinessCaches} from '../../web/src/state/cache-lifecycle.mjs';
import {migrateChatCache} from '../../pkg/storage/migrations/ui-cache/0001_chat_cache.mjs';
import {migrateTaskCheckpoints} from '../../pkg/storage/migrations/ui-cache/0004_task_checkpoints.mjs';
import {assistantDraftAttachments} from './assistant-drafts-attachments.mjs';

const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve};};
const remove=name=>new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});
export async function assistantDraftStorage(report=()=>{}){
 const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
 for(const version of [1,4]){
  const name='draft-upgrade-'+version+'-'+crypto.randomUUID(),old=createIndexedStore({name,version,migrate:version===1?migrateChatCache:migrateTaskCheckpoints});
  await old.transaction(['conversations','messages','views'],'readwrite',tx=>{
   tx.objectStore('conversations').put({key:'old-chat',conversation_id:'main'});
   tx.objectStore('messages').put({key:'old-chat',id:'message',order:'0001',bytes:12,message:{text:'Keep old chat'}});
   tx.objectStore('views').put({key:'old-chat',surface:'browser',position:{id:'message'}});
  });old.close();
  let upgraded=createIndexedStore({name});
  check(await upgraded.transaction(['messages','views','composerDrafts','composerFiles'],'readonly',async tx=>(await idbRequest(tx.objectStore('messages').get(['old-chat','message']))).message.text==='Keep old chat'&&(await idbRequest(tx.objectStore('views').get(['old-chat','browser']))).position.id==='message'&&(await idbRequest(tx.objectStore('composerDrafts').count()))===0),'v'+version+' upgrade preserves messages and reading positions and creates empty draft stores');
  upgraded.close();const drafts=createDraftStore({name});await drafts.write('assistant',{conversation_id:'main',text:'Retain me'},[{id:'image',blob:new Blob(['original'],{type:'image/png'})}]);drafts.close();
  const failed=createIndexedStore({name,version:UI_CACHE_VERSION+1,migrate(db){db.createObjectStore('must-roll-back');throw Error('Synthetic migration failure');}});let rejected=false;
  try{await failed.transaction(['composerDrafts'],'readonly',()=>{});}catch(error){rejected=error.message==='Synthetic migration failure';}failed.close();
  const reopened=createDraftStore({name});check(rejected&&(await reopened.read('assistant')).files[0].blob.size===8,'Failed migration rolls back and permits reopening without losing drafts');reopened.close();
  await clearBusinessCaches({name});const retained=createDraftStore({name});check((await retained.read('assistant')).record.text==='Retain me','Business cache cleanup preserves unsent drafts and originals');retained.close();await remove(name);await remove(name+'-tasks');
 }
 const name='draft-races-'+crypto.randomUUID(),storage=createDraftStore({name}),client={runtime:{runtime_id:'host'},spaces:[{id:'one',control_id:'control'},{id:'two',control_id:'other-control'}]},scope={space:'one',owner:'a'},key=assistantDraftKey(client,scope);
 const snapshot={runtime_id:'host',owner_id:'["one","a"]',conversation_id:'main',boot_id:'boot',running:false,turn_id:null,messages:[]},state=createConversationState();state.snapshot=snapshot;
 const stateKey=ownerKey(snapshot),oldRef={message_id:'source',quote:'old excerpt'},blob=new Blob(['image'],{type:'image/png'});
 await storage.write(key,{conversation_id:'main',state_key:stateKey,text:'Old text',references:[oldRef],draftVersion:5,referenceVersion:7,attachments:[{attachment:{id:'original',name:'a.png',mime_type:'image/png',size:5},phase:'failed',error:'Synthetic failure'}],pending:null},[{id:'original',blob}]);
 const load=deferred(),started=deferred();let items=[],restored=0;
 const attachments={items:()=>items,hydrate(_key,records,files){items=records.map(record=>({...record,file:files.find(file=>file.id===record.attachment.id)?.blob}));},discard(){items=[];}};
 const controller=createAssistantDrafts({client,state,attachments,changed(){},restored(){restored++;},restorePending(){},storage:{...storage,close(){},async read(key){started.resolve();await load.promise;return storage.read(key);}}});state.draftChanged=key=>controller.change(key);
 controller.activate(snapshot,scope);await started.promise;setDraft(state,'Typed during load');setDraftReferences(state,[{message_id:'new-source'}]);load.resolve();await controller.flush(stateKey);
 let saved=await storage.read(key);check(saved.record.text==='Typed during load'&&saved.record.references[0].message_id==='new-source'&&saved.record.draftVersion>5&&saved.record.referenceVersion>7&&restored===1,'Late disk load preserves newer text and references with distinct versions');
 check(saved.files[0].blob.size===5&&items[0].phase==='failed','Late load still recovers the original Blob and failed upload');
 const put=IDBObjectStore.prototype.put;let fileWrites=0;IDBObjectStore.prototype.put=function(...args){if(this.name==='composerFiles')fileWrites++;return put.apply(this,args);};
 try{setDraft(state,'Typing one');await controller.flush(stateKey);setDraft(state,'Typing two');await controller.flush(stateKey);check(fileWrites===0,'Typing commits metadata without rewriting existing Blobs');}finally{IDBObjectStore.prototype.put=put;}
 const send=beginSend(state,'fixed-request',{attachments:items.map(item=>item.attachment)});controller.change(stateKey,true);await controller.flush(stateKey);check((await storage.read(key)).record.pending.request.request_id==='fixed-request','Immutable send request is committed before transport can start');
 setDraft(state,'New after submit');items.push({attachment:{id:'new-image',name:'b.png',size:3,mime_type:'image/png'},file:new Blob(['new']),phase:'waiting'});controller.change(stateKey);
 settleSend(state,send,true);items=items.filter(item=>item.attachment.id!=='original');controller.change(stateKey,true);await controller.flush(stateKey);saved=await storage.read(key);
 check(saved.record.text==='New after submit'&&saved.record.pending===null&&saved.files.length===1&&saved.files[0].id==='new-image','Acceptance clears only submitted IDs and retains new text and Blob');
 const nativePut=IDBObjectStore.prototype.put;
 IDBObjectStore.prototype.put=function(...args){const request=nativePut.apply(this,args);if(this.name==='composerDrafts')request.addEventListener('success',()=>this.transaction.abort());return request;};
 let aborted=false;try{await storage.write(key,{text:'Must roll back'},[{id:'abort-file',blob}]);}catch{aborted=true;}finally{IDBObjectStore.prototype.put=nativePut;}
 saved=await storage.read(key);check(aborted&&saved.record.text==='New after submit'&&saved.files[0].id==='new-image','A put success followed by abort preserves the previous draft and its Blob atomically');
 await storage.write('other-assistant',{text:'Other'},[{id:'other',blob}]);await controller.forget(scope);check(await storage.read(key)===null&&!state.drafts.has(stateKey)&&!state.referenceDrafts.has(stateKey)&&(await storage.read('other-assistant')).files.length===1,'Assistant deletion removes its own memory, record and originals without affecting others');controller.close();
 const pendingRead=deferred(),readStarted=deferred(),newState=createConversationState();let publish=0;
 const stale=createAssistantDrafts({client,state:newState,attachments:{items:()=>[],hydrate(){publish++;},discard(){}},changed(){},restored(){publish++;},restorePending(){},storage:{...storage,close(){},async read(key){readStarted.resolve();await pendingRead.promise;return storage.read(key);}}});
 stale.activate(snapshot,scope);await readStarted.promise;await stale.forget(scope);pendingRead.resolve();await stale.flush(stateKey);await Promise.resolve();check(publish===0&&await storage.read(key)===null,'Deletion retires a late load without resurrecting its draft');stale.close();
 const held=deferred(),writeStarted=deferred(),busyState=createConversationState();busyState.snapshot=snapshot;let writes=0;
 const busy=createAssistantDrafts({client,state:busyState,attachments:{items:()=>[],hydrate(){},discard(){}},changed(){},restored(){},restorePending(){},storage:{...storage,close(){},async write(...args){const result=storage.write(...args);if(writes++===0){writeStarted.resolve();await held.promise;}return result;}}});busyState.draftChanged=key=>busy.change(key);
 busy.activate(snapshot,scope);await busy.flush(stateKey);setDraft(busyState,'Submitted before delayed save');const attempt=beginSend(busyState,'delayed-request');busy.change(stateKey,true);const saving=busy.flush(stateKey);await writeStarted.promise;
 check(busy.status(stateKey).dirty&&busy.status(stateKey).saving,'A hung write remains visibly unsaved');
 settleSend(busyState,attempt,true);setDraft(busyState,'New typing after confirmation');busy.change(stateKey,true);held.resolve();await saving;await busy.flush(stateKey);saved=await storage.read(key);
 check(saved.record.text==='New typing after confirmation'&&saved.record.pending===null,'A late old write cannot resurrect an accepted snapshot or overwrite newer typing');busy.close();
 await storage.write(key,{conversation_id:'main',text:'Disk before read failure',references:[],draftVersion:20,referenceVersion:0,attachments:[],pending:null});
 const retryState=createConversationState();retryState.snapshot=snapshot;let readFailure=true;
 const retry=createAssistantDrafts({client,state:retryState,attachments:{items:()=>[],hydrate(){},discard(){}},changed(){},restored(){},restorePending(){},storage:{...storage,close(){},read(key){return readFailure?Promise.reject(Error('Synthetic read failure')):storage.read(key);}}});retryState.draftChanged=key=>retry.change(key);
 retry.activate(snapshot,scope);try{await retry.flush(stateKey);}catch{}setDraft(retryState,'Typed after read failure');readFailure=false;await retry.retry(stateKey);
 check((await storage.read(key)).record.text==='Typed after read failure','Retrying a failed load preserves edits made before Retry');retry.close();
 check(assistantDraftKey(client,{space:'two',owner:'a'})!==key&&assistantDraftKey({...client,spaces:[],runtime:{runtime_id:'another'}},scope)!==key,'Draft identities include Control, Space and assistant');
 storage.close();await remove(name);
 const unavailable=createDraftStore({indexedDB:null});let explicit=false;try{await unavailable.read('x');}catch(error){explicit=error.message==='chat_cache_unavailable';}unavailable.close();check(explicit,'Unavailable IndexedDB is an explicit failure');
 await assistantDraftAttachments(report);
 return true;
}
