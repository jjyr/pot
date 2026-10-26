import {createIndexedStore,idbRequest} from '../../web/src/state/indexed-store.mjs';
import {clearBusinessCaches} from '../../web/src/state/cache-lifecycle.mjs';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createProjectionStore} from '../../web/src/state/projection-store.mjs';
import {createResourceReads,displayProjection} from '../../web/src/state/resource-cache.mjs';
import {createTaskRepository} from '../../web/src/state/task-repository.mjs';
import {createChatCache} from '../../web/src/state/chat-cache.mjs';
import {migrateChatCache} from '../../pkg/storage/migrations/ui-cache/0001_chat_cache.mjs';
import {UI_CACHE_VERSION} from '../../pkg/storage/migrations/ui-cache/0005_assistant_drafts.mjs';
const remove=name=>new Promise((resolve,reject)=>{const op=indexedDB.deleteDatabase(name);op.onsuccess=resolve;op.onerror=()=>reject(op.error);});
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};};
export async function projectionStoreContract(report=()=>{}){
  const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
  const name='pot-projection-contract-'+crypto.randomUUID(),meta={control:'node',space:'local',owner:'alice',boot:'boot',path:'/api/v1/assistants',topics:['assistants']};
  const first=createProjectionStore({name}),second=createProjectionStore({name});let count=0;
  const stop=first.subscribe(()=>count++);
  try{
    await first.register(meta);const token=await first.token(meta);await first.commit('a',meta,{items:[{id:'alice'}]},token);
    check((await first.read('a')).value.items[0].id==='alice'&&count===1,'Committed data precedes notification');
    const put=IDBObjectStore.prototype.put;
    IDBObjectStore.prototype.put=function(...args){const op=put.apply(this,args);if(this.name==='projections')op.addEventListener('success',()=>this.transaction.abort());return op;};
    try{let failed=false;try{await first.commit('a',meta,{items:[{id:'uncommitted'}]},token);}catch(error){failed=error.storage;}check(failed,'Aborted writes report storage failure');}finally{IDBObjectStore.prototype.put=put;}
    check((await first.read('a')).value.items[0].id==='alice'&&count===1,'Request success followed by transaction abort neither publishes nor loses committed data');
    await second.invalidate(['assistants'],meta);
    check(!await first.commit('a',meta,{items:[{id:'late HTTP'}]},token),'Cross-client event fences older in-flight HTTP result');
    const beforeCommand=await first.token(meta);await second.command('receipt',meta,{id:'accepted'});check(!await first.commit('late-command',meta,{},beforeCommand),'Atomic mutation receipt and epoch fence an older HTTP response');
    const announced=deferred(),off=second.subscribe(event=>{if(event.key==='b')announced.resolve();});
    await first.commit('b',{...meta,owner:'bob'},{items:[{id:'bob'}]},await first.token(meta));await announced.promise;off();
    check((await second.read('b')).value.items[0].id==='bob','Cross-tab commit notification queries stored content');
    const sanitized=displayProjection('/api/v1/providers',{profiles:[],api_key:'secret',provider_types:[{id:'public',default_credential:'do-not-copy',credentials:{token:'private'}}]});
    check(!JSON.stringify(sanitized).includes('secret')&&!JSON.stringify(sanitized).includes('do-not-copy')&&sanitized.provider_types[0].default_credential===true,'Provider projection retains capability without credential bytes');
    const largeResult=displayProjection('/api/v1/tasks/task',{result:{text:'r'.repeat(2*1024*1024)}}),inputs=displayProjection('/api/v1/tasks/task/messages',{items:Array.from({length:300},(_,id)=>({request_id:String(id),text:'input'}))});check(largeResult.cache_truncated&&largeResult.result.text.length===1024*1024&&inputs.cache_truncated&&inputs.items.length===256,'Task result metadata and guidance projections are bounded and explicitly marked');
    stop();const previous=count;await first.commit('c',meta,{},await first.token(meta));check(count===previous,'Disposed query listener receives no commit');
    const bounded=createProjectionStore({name,maxEntries:2});await bounded.commit('d',meta,{},await bounded.token(meta));const old=await bounded.read('a');check(!old,'Projection LRU enforces entry budget');bounded.close();
  }finally{stop();first.close();second.close();await remove(name);}
  const resourceName='pot-resource-contract-'+crypto.randomUUID(),clients=[];
  const make=(node,owner,transport)=>{const client={production:true,runtime:{runtime_id:node,boot_id:'boot'},scope:()=>({space:'local',owner}),transport};const reads=createResourceReads(client,{storage:createProjectionStore({name:resourceName})});clients.push(reads);return reads;};
  try{
    const a=make('node-a','alice',async path=>({items:[{id:'a:'+path}]})),b=make('node-a','bob',async()=>({items:[{id:'bob'}]})),c=make('node-b','alice',async()=>({items:[{id:'node-b'}]}));
    await a.read('/api/v1/assistants?q=one');await a.read('/api/v1/assistants?q=two');await b.read('/api/v1/assistants?q=one');await c.read('/api/v1/assistants?q=one');
    check((await a.query('/api/v1/assistants?q=one')).value.items[0].id.endsWith('q=one')&&(await a.query('/api/v1/assistants?q=two')).value.items[0].id.endsWith('q=two')&&(await b.query('/api/v1/assistants?q=one')).value.items[0].id==='bob'&&(await c.query('/api/v1/assistants?q=one')).value.items[0].id==='node-b','Stored queries isolate node, assistant and full query');
    a.clear();const gate=deferred(),updated=deferred(),cold=make('node-a','alice',async()=>{await gate.promise;return {items:[{id:'confirmed'}]};});const stop=cold.subscribe(()=>updated.resolve());
    const retained=await cold.read('/api/v1/assistants?q=one');check(retained.items[0].id.endsWith('q=one')&&cold.state(retained).freshness==='retained','Cold repository renders committed retained data before network confirmation');gate.resolve();await updated.promise;stop();
    const fresh=await cold.read('/api/v1/assistants?q=one');check(fresh.items[0].id==='confirmed'&&cold.state(fresh).freshness==='fresh'&&cold.state(fresh).lastConfirmed>0,'Cold refresh publishes committed fresh data with confirmation time');
    const epochPut=IDBObjectStore.prototype.put;IDBObjectStore.prototype.put=function(...args){if(this.name==='epochs')throw new DOMException('Synthetic invalidation quota','QuotaExceededError');return epochPut.apply(this,args);};cold.invalidate(['assistants'],{space:'local'});let invalidationFailed=false;try{await cold.read('/api/v1/assistants?q=one',{authoritative:true});}catch(error){invalidationFailed=error.storage;}finally{IDBObjectStore.prototype.put=epochPut;}
    const afterInvalidation=await cold.storage.read(JSON.stringify(cold.describe('/api/v1/assistants?q=one')));check(invalidationFailed&&afterInvalidation?.value.items[0].id==='confirmed','Failed invalidation cannot publish a volatile refresh or erase committed content');
    check((await cold.read('/api/v1/assistants?q=one',{authoritative:true})).items[0].id==='confirmed','A later read retries the pending invalidation and recovers from storage failure');
    let policyFail=false;const policyReads=make('node-policy','',async()=>{if(policyFail)throw Error('Offline permissions');return {policy:{machines:{fixture:{revision:1}}},pending:[{id:'must-not-persist',token:'private-approval'}]};});await policyReads.policySummary();policyFail=true;let policyRejected=false;try{await policyReads.policySummary();}catch{policyRejected=true;}const policySaved=await policyReads.query('@permissions-policy',{collection:true});check(policyRejected&&policySaved.value.policy.machines.fixture.revision===1&&!JSON.stringify(policySaved.value).includes('must-not-persist'),'Permission failure retains only a policy display summary and never pending approvals');
    const budget=createProjectionStore({name:resourceName,maxBytes:32});const meta=cold.describe('/api/v1/assistants');await budget.commit('budget',meta,{id:'old'},await budget.token(meta));let overflow=false;try{await budget.commit('budget',meta,{id:'x'.repeat(100)},await budget.token(meta));}catch(error){overflow=error.storage;}check(overflow&&(await budget.read('budget')).value.id==='old','Byte budget failure preserves the committed value');budget.close();
    const expired=createProjectionStore({name:resourceName,retention:-1});await expired.commit('expired',meta,{},await expired.token(meta));check(await expired.read('expired')===null,'Expired projections cannot bootstrap a view');expired.close();
  }finally{for(const reads of clients)reads.clear();await remove(resourceName);}
  const commandName='pot-command-contract-'+crypto.randomUUID(),originalFetch=globalThis.fetch;let posts=0,transport;
  globalThis.fetch=async(path,options={})=>{if(options.method==='POST')posts++;return new Response(JSON.stringify(path.startsWith('/api/v1/runtime')?{runtime_id:'commands',boot_id:'boot'}:{id:'profile',revision:2,name:'Saved',api_key:'never-persist'}));};
  try{
    transport=await createProductionClient({resourceStorage:createProjectionStore({name:commandName})});transport.setScope({space:'local',owner:'alice'});
    const put=IDBObjectStore.prototype.put;IDBObjectStore.prototype.put=function(...args){if(this.name==='projections')throw new DOMException('Synthetic quota limit','QuotaExceededError');return put.apply(this,args);};let failed=false;
    try{await transport.request('/api/v1/providers',{method:'POST',body:{request_id:'same',api_key:'input-private'}});}catch(error){failed=error.storage&&error.accepted;}finally{IDBObjectStore.prototype.put=put;}
    check(failed&&posts===1,'Accepted command reports quota failure without publishing its response');
    const saved=await transport.request('/api/v1/providers',{method:'POST',body:{request_id:'same',api_key:'input-private'}});check(posts===1&&saved.id==='profile'&&!JSON.stringify(saved).includes('never-persist'),'Manual command retry commits the accepted response without resending the side effect');
    const db=createIndexedStore({name:commandName});const contents=await db.transaction(['projections'],'readonly',tx=>idbRequest(tx.objectStore('projections').getAll()));check(!JSON.stringify(contents).includes('input-private')&&!JSON.stringify(contents).includes('never-persist'),'Command receipts contain neither request credentials nor secret response fields');db.close();
    await clearBusinessCaches({name:commandName});let retired=false;try{await transport.resources.storage.commit('late',{control:'commands',space:'local',topics:[]},{},'[0]');}catch{retired=true;}check(retired,'Logout retires old repository writers');
    const reopened=createProjectionStore({name:commandName});check(await reopened.read(contents[0].key)===null,'Logout clears committed business projections');reopened.close();
  }finally{transport?.resources.clear();globalThis.fetch=originalFetch;await remove(commandName);await remove(commandName+'-tasks');}
  const upgradeName='pot-upgrade-abort-'+crypto.randomUUID(),before=createIndexedStore({name:upgradeName});
  await before.transaction(['cacheStats'],'readwrite',tx=>tx.objectStore('cacheStats').put({key:'preserved',value:42}));
  const broken=createIndexedStore({name:upgradeName,version:UI_CACHE_VERSION+1,migrate:(db,tx)=>{db.createObjectStore('uncommitted');tx.abort();}});let aborted=false;try{await broken.transaction(['cacheStats'],'readonly',()=>{});}catch{aborted=true;}broken.close();
  const retry=createIndexedStore({name:upgradeName});check(aborted&&(await retry.transaction(['cacheStats'],'readonly',tx=>idbRequest(tx.objectStore('cacheStats').get('preserved')))).value===42,'Versionchange closes old connections and aborted upgrades preserve the previous version for retry');before.close();retry.close();await remove(upgradeName);
  const taskName='pot-task-contract-'+crypto.randomUUID(),client={runtime:{runtime_id:'task-node',boot_id:'boot'},chatCacheName:taskName,request:()=>{throw Error('Unexpected history fetch');}},tasks=createTaskRepository(client),task={id:'task',run_id:'run'},scope={space:'local',owner:'alice'};
  try{
    let sequence=0;
    for(let page=0;page<20;page++){
      const events=[];for(let i=0;i<50;i++){const id=String(sequence);events.push({sequence:++sequence,event:{update:{type:'message_started',id}}},{sequence:++sequence,event:{update:{type:'text_delta',text:'x'.repeat(4096)}}},{sequence:++sequence,event:{update:{type:'message_finished'}}});}
      await tasks.append(task,scope,{events});
    }
    const saved=await tasks.read(task,scope);check(saved.cursor===3000&&saved.messages.length<=640,'Task checkpoint has bounded current window and atomic event cursor');
    const original=saved.messages.at(-1).text;let rejected=false;
    try{await tasks.append(task,scope,{events:[{sequence:3002,event:{update:{type:'text_delta',text:'bad gap'}}}]});}catch{rejected=true;}
    check(rejected&&(await tasks.read(task,scope)).cursor===3000&&saved.messages.at(-1).text===original,'Invalid task sequence cannot mutate a committed view or advance its checkpoint');
    const historical=await tasks.earlier(task,scope,saved.messages[0].sequence);check(historical.messages.length<=80&&historical.cursor===3000,'Task historical query does not replace live cursor');
    await tasks.append(task,scope,{events:[{sequence:3001,event:{update:{type:'message_started',id:'large'}}},{sequence:3002,event:{update:{type:'text_delta',text:'x'.repeat(2*1024*1024)}}}]});
    const large=await tasks.read(task,scope);check(large.cache_truncated&&large.active.text.length===1024*1024,'Oversized task text is bounded and explicitly marked');
    await tasks.append(task,scope,{events:[{sequence:3003,event:{update:{type:'message_finished'}}},{sequence:3004,event:{update:{type:'tool_started',id:'bounded-tool',name:'exec_command',args:{cmd:'x'.repeat(2*1024*1024)}}}},{sequence:3005,event:{update:{type:'tool_output',id:'bounded-tool',stream:'stdout',text:'a'.repeat(800000)}}},{sequence:3006,event:{update:{type:'tool_output',id:'bounded-tool',stream:'stderr',text:'b'.repeat(800000)}}}]});
    const toolBefore=await tasks.read(task,scope),tool=toolBefore.messages.find(message=>message.tool?.id==='bounded-tool');check(tool.cache_truncated&&tool.tool.output.reduce((total,chunk)=>total+chunk.text.length,0)===1024*1024&&tool.tool.args.length===1024*1024,'Task tool arguments and combined output streams obey explicit bounds');
    await tasks.append(task,scope,{events:[{sequence:3007,event:{update:{type:'tool_output',id:'bounded-tool',stream:'stderr',text:'later'}}},{sequence:3008,event:{update:{type:'tool_finished',id:'bounded-tool',result:{data:'r'.repeat(2*1024*1024)}}}}]});
    const toolAfter=await tasks.read(task,scope);check(!toolBefore.messages.find(message=>message.tool?.id==='bounded-tool').tool.output.at(-1).text.endsWith('later')&&toolAfter.tools.size===0&&toolAfter.messages.find(message=>message.tool?.id==='bounded-tool').tool.result.length===1024*1024,'Tool updates preserve committed views and completed tools leave the active checkpoint');
    const checkpointPut=IDBObjectStore.prototype.put;IDBObjectStore.prototype.put=function(...args){if(this.name==='taskCheckpoints')throw new DOMException('Synthetic checkpoint quota','QuotaExceededError');return checkpointPut.apply(this,args);};let checkpointFailed=false;try{await tasks.append(task,scope,{events:[{sequence:3009,event:{update:{type:'message_started',id:'uncommitted'}}}]});}catch(error){checkpointFailed=error.storage;}finally{IDBObjectStore.prototype.put=checkpointPut;}check(checkpointFailed&&(await tasks.read(task,scope)).cursor===3008&&!(await tasks.read(task,scope)).messages.some(message=>message.id==='message-uncommitted'),'Task checkpoint quota failure rolls back message bodies and cursor with a localized storage failure');
    const other={...task,run_id:'new-run'};check(await tasks.read(other,scope)===null,'A different task run cannot inherit a checkpoint');
    await tasks.append(other,scope,{events:[{sequence:1,event:{update:{type:'message_started',id:'new'}}}]});check((await tasks.read(other,scope)).cursor===1,'A fresh run resets its own transcript without replaying commands');
  }finally{tasks.close();await remove(taskName+'-tasks');}
  const legacy='pot-upgrade-contract-'+crypto.randomUUID();
  const db=await new Promise((resolve,reject)=>{const op=indexedDB.open(legacy,1);op.onupgradeneeded=()=>migrateChatCache(op.result,op.transaction,0);op.onsuccess=()=>resolve(op.result);op.onerror=()=>reject(op.error);});
  const message={id:'m1',sequence:'1',version:1,role:'user',text:'Preserved legacy message',status:'complete'};
  await new Promise((resolve,reject)=>{const tx=db.transaction(['messages','views','conversations'],'readwrite');tx.objectStore('messages').put({key:'legacy',id:'m1',order:'1'.padStart(24,'0'),message,normal:true,bytes:512,used:1});tx.objectStore('conversations').put({key:'legacy',schema_version:1,projection:{runtime_id:'node',owner_id:'alice',conversation_id:'chat',boot_id:'boot',cursor:'1',running:false,turn_id:null},replies:[],window:[{id:'m1'}],updates:[],latest:'1',ranges:[{before:'1',after:'1'}],bytes:512,used:1});tx.objectStore('views').put({key:'legacy',surface:'main',conversation_id:'chat',boot_id:'boot',cursor:'1',window:[{id:'m1'}],position:{mode:'reading'},tools:[['call',{open:true}]],used:Date.now()});tx.oncomplete=resolve;tx.onerror=()=>reject(tx.error);});db.close();
  const upgraded=createChatCache({name:legacy});try{const saved=await upgraded.read('legacy','main');check(saved.snapshot.messages[0].text===message.text&&saved.view.tools[0][1].open,'Skipped v1→current upgrade preserves history and reading disclosures');}finally{upgraded.close();await remove(legacy);}
  report('DONE projection store contract');
}
