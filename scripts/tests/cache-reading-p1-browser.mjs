import {createTasksDomain} from '../../web/src/state/domains/tasks.mjs';
import {createQueryRegistry,createCommands} from '../../web/src/state/domains/query.mjs';
import {createChatCache} from '../../web/src/state/chat-cache.mjs';
import {createChatDomain} from '../../web/src/state/domains/chat.mjs';
import {createTaskRepository} from '../../web/src/state/task-repository.mjs';
import {createResourceReads} from '../../web/src/state/resource-cache.mjs';
import {createPages} from '../../web/src/components/pages.mjs';
import {normalizeSnapshot} from '../../web/src/adapters/production.mjs';
const output=document.querySelector('#results'),host=document.querySelector('#page');
const report=text=>output.textContent+='PASS '+text+'\n';
const check=(value,text)=>{if(!value)throw Error(text);report(text);};
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const wait=async(test,label)=>{for(let i=0;i<250;i++){if(test())return;await pause(20);}throw Error('Timeout '+label);};
const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve};};
const caps=operations=>({version:1,operations}),scope={space:'s',owner:'o'};
const name='pot-p1-browser-'+crypto.randomUUID(),names=[name,name+'-tasks',name+'-resources'];
const runtime={runtime_id:'r',boot_id:'b'},posts=[],metadata=deferred(),machine=deferred();
let metadataEntered=false,machineEntered=false,holdSnapshot,holdResume,wrongTurn=false,snapshotReads=0,language='en';
const task={id:'t',run_id:'one',title:'Retained task',phase:'running',assistant_id:'o',_space_id:'s'};
const frame=()=>({kind:'reset',base_cursor:0,view:{state:{cursor:1,boot_id:'b',conversation_id:'c',turn:{user_message_id:wrongTurn?'other':'input'}},messages:[{sequence:'1',version:1,message:{id:'input',role:'user',request_id:'wait-request',text:'fixture'}}],replies:[],history:{}}});
const client={production:true,chatCacheName:name,runtime,spaces:[{id:'s',control_id:'r',name:'Fixture',connected:true}],assistants:[{id:'o',space_id:'s',name:'Owner',capabilities:caps(['chat.read','chat.cancel','assistant.usage'])}],scope:()=>({...scope}),operationScopeReady:()=>true,sessionReady:()=>true,supports:()=>true,
  snapshot:async()=>{snapshotReads++;if(holdSnapshot)await holdSnapshot.promise;return normalizeSnapshot(frame(),runtime,'o','s');},
  async request(path,options={}){
    const url=new URL(path,location.origin);
    if(options.method==='POST'){posts.push({path,...options});if(path==='/api/v1/usage/resume'&&holdResume)await holdResume.promise;return {};}
    if(url.pathname==='/api/v1/tasks/t'){metadataEntered=true;await metadata.promise;return {run_id:'two',info:task,capabilities:caps(['task.read','task.events','task.cancel'])};}
    if(url.pathname==='/api/v1/runtime'){machineEntered=true;await machine.promise;return {runtime_id:'r',host:{hostname:'Fixture'}};}
    if(url.pathname==='/api/v1/usage/summary')return {total:{input:1,output:1,cache:0},lifetime:{input:1,output:1,cache:0},quota:{policy:{period:'none'},active:false},waiting:{turn:'wait-request',since:42},buckets:[]};
    if(url.pathname==='/api/v1/permissions')return {pending:[],policy:{}};
    if(url.pathname.endsWith('/events'))return {run_id:'two',events:[],has_more:false};
    return {items:[],next_offset:null};
  },downloadFile:()=>{},
};
let pages,domain,session,cache;
const button=text=>[...host.querySelectorAll('button')].find(node=>node.textContent===text);
window.addEventListener('unhandledrejection',event=>{output.textContent+='UNHANDLED '+event.reason?.stack+'\n';});
async function run(){
  output.textContent='';
  try{
    // Real IDB committed records, then deny all subsequent readwrite transactions.
    cache=createChatCache({name});const key=JSON.stringify(['r','s','o']),messages=Array.from({length:160},(_,i)=>({id:'m'+(i+1),sequence:String(i+1),version:1,role:'assistant',text:'committed '+(i+1),status:'complete'}));
    await cache.writeFrames(key,[{runtime_id:'r',owner_id:JSON.stringify(['s','o']),conversation_id:'c',boot_id:'b',kind:'reset',base_cursor:'0',cursor:'1',messages,replies:[],running:false,history:{before:'1',after:'160',has_more:false,has_newer:false}}]);
    const chatClient={...client,history:async()=>({boot_id:'b',conversation_id:'c',cursor:'1',messages:[{id:'network',sequence:'161',version:1,role:'assistant',text:'uncommitted'}],history:{before:'161',after:'161'}})};
    domain=createChatDomain(chatClient);session=domain.open(scope);await session.query();
    const original=IDBDatabase.prototype.transaction;
    IDBDatabase.prototype.transaction=function(stores,mode,...rest){if(mode==='readwrite')throw new DOMException('Synthetic write failure','QuotaExceededError');return original.call(this,stores,mode,...rest);};
    try{
      const cached=await session.history({around:'m20',limit:20},{around:true,cachedOnly:true});check(cached.messages.some(row=>row.id==='m20'),'real IDB cached Chat history projects with every readwrite transaction denied');
      let failed=false;try{await session.saveView({position:{mode:'reading'},layout:'fixture'});}catch(error){failed=error.storage;}check(failed,'view hint write reports its own local storage failure');
      check((await session.history({around:'m30',limit:20},{around:true,cachedOnly:true})).messages.some(row=>row.id==='m30'),'failed position write does not hide a second cached window');
      failed=false;try{await session.history({after:'160'});}catch(error){failed=error.storage;}check(failed,'network history cannot publish before a successful commit');
    }finally{IDBDatabase.prototype.transaction=original;}
    await session.saveView({position:{mode:'reading'},layout:'fixture-recovered'});report('view hint explicitly retries after storage recovery');
    await session.close();session=null;domain.dispose();domain=null;cache.close();cache=null;

    // Real resource store: failed namespace write and pending epoch do not hide read.
    const resources=createResourceReads({...client,chatCacheName:name+'-resources'});
    await resources.read('/api/v1/usage/summary',scope);resources.clear();
    const offline={...client,chatCacheName:name+'-resources',onlineReady:false},retained=createResourceReads(offline);
    const originalTx=IDBDatabase.prototype.transaction;
    IDBDatabase.prototype.transaction=function(stores,mode,...rest){if(mode==='readwrite')throw new DOMException('Synthetic write failure','QuotaExceededError');return originalTx.call(this,stores,mode,...rest);};
    try{await retained.invalidate(['usage'],{space:'s'});const value=await retained.read('/api/v1/usage/summary',scope);check(value.total.input===1&&retained.state(value).freshness!=='fresh','real IDB committed resource remains retained while epoch writes fail');let failed=false;try{await retained.read('/api/v1/usage/summary',{...scope,authoritative:true});}catch(error){failed=error.storage;}check(failed,'failed epoch cannot authorize a fresh resource');}finally{IDBDatabase.prototype.transaction=originalTx;retained.clear();}

    // Actual domain, scheduler and Task repository: no new server event wakes it.
    {
      const events=[{sequence:1,event:{update:{type:'message_started',id:'long'}}},...Array.from({length:6201},(_,i)=>({sequence:i+2,event:{update:{type:'text_delta',text:'x'}}})),{sequence:6203,event:{update:{type:'message_finished'}}}];
      const snapshots=[];let reads=0;
      const resources={read:async()=>({run_id:'long',revision:1,capabilities:caps(['task.events'])}),state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
      const longClient={...client,request:async path=>{reads++;const after=Number(new URL(path,location.origin).searchParams.get('after'));return {run_id:'long',events:events.slice(after,after+100),has_more:after+100<events.length};}};
      const queries=createQueryRegistry(resources),commands=createCommands(longClient),tasks=createTasksDomain(longClient,{resources,queries,commands});
      const stop=tasks.watchDetail({id:'long',run_id:'long'},scope,value=>snapshots.push({cursor:value.data?.state.cursor,fresh:value.fresh,catchingUp:value.data?.catchingUp}));
      await wait(()=>snapshots.at(-1)?.fresh&&snapshots.at(-1).cursor===6203,'real committed 6203 journal');
      check(reads===63&&snapshots.some(value=>value.cursor===5000&&value.catchingUp&&!value.fresh),'real IDB Task journal catches up 6203 events in 63 serial pages with no premature fresh');
      stop();tasks.dispose();queries.dispose();commands.dispose();
    }
    const repository=createTaskRepository(client);
    const fenced={id:'fenced',run_id:'fenced'},seed=[{sequence:1,event:{update:{type:'message_started',id:'fenced'}}}];
    await repository.append(fenced,scope,{run_id:'fenced',events:seed});
    async function queuedWrite(work,retire){
      let releaseGate=false,blocked=false;const gateReady=deferred(),originalAdmission=IDBDatabase.prototype.transaction;
      IDBDatabase.prototype.transaction=function(stores,mode,...rest){
        if(mode==='readwrite'&&!blocked){blocked=true;const gate=originalAdmission.call(this,['conversations'],'readwrite');const pump=()=>{const op=gate.objectStore('conversations').get('fixture-gate');op.onsuccess=()=>{gateReady.resolve();if(!releaseGate)pump();};};pump();}
        return originalAdmission.call(this,stores,mode,...rest);
      };
      try{const pending=work();await gateReady.promise;retire();releaseGate=true;let rejected=false;try{await pending;}catch(error){rejected=error.name==='AbortError';}return rejected;}
      finally{releaseGate=true;IDBDatabase.prototype.transaction=originalAdmission;}
    }
    for(const change of ['view','serving-boot','source-boot']){
      let allowed=true;
      const rejected=await queuedWrite(()=>repository.append(fenced,scope,{run_id:'fenced',events:[{sequence:2,event:{update:{type:'text_delta',text:'must not commit'}}}]},{validate:()=>{if(!allowed)throw new DOMException('Fixture retired','AbortError');}}),()=>{if(change==='view')allowed=false;if(change==='serving-boot')client.runtime.boot_id='retired';if(change==='source-boot')client.spaces[0].boot_id='retired';});
      client.runtime.boot_id='b';delete client.spaces[0].boot_id;
      check(rejected&&(await repository.read(fenced,scope)).cursor===1,'Task '+change+' retired while queued behind real IDB performs zero commit');
    }
    const actualRequest=client.request;let historicalRun='fenced',historyReads=0;
    client.request=async()=>{historyReads++;return {run_id:historicalRun,events:[{sequence:1,event:{update:{type:'message_started',id:'history-only'}}},{sequence:2,event:{update:{type:'message_finished'}}}],has_more:false};};
    for(const source of [undefined,'other']){
      historicalRun=source;let rejected=false;try{await repository.earlier(fenced,scope,4);}catch(error){rejected=error.code==='task_run_changed';}
      check(rejected&&(await repository.read(fenced,scope)).cursor===1,'missing or mismatched history run performs zero records commit');
    }
    historicalRun='fenced';const retiredHistory=await queuedWrite(()=>repository.earlier(fenced,scope,4),()=>{client.runtime.boot_id='retired';});client.runtime.boot_id='b';
    check(retiredHistory&&(await repository.read(fenced,scope)).cursor===1,'historical backfill boot change during real IDB wait aborts records and coverage together');
    client.request=actualRequest;
    let wrongSource=false;try{await repository.append(fenced,scope,{run_id:'other',events:[{sequence:2,event:{update:{type:'text_delta',text:'wrong run'}}}]});}catch(error){wrongSource=error.code==='task_run_changed';}
    check(wrongSource&&(await repository.read(fenced,scope)).cursor===1,'same-sequence page from another run cannot enter the actual repository');

    const continued={id:'continued',run_id:'one'},oldRows=[{sequence:1,event:{turn_id:'one',update:{type:'message_started',id:'old-run'}}},{sequence:2,event:{turn_id:'one',update:{type:'text_delta',text:'old committed run'}}},{sequence:3,event:{turn_id:'one',update:{type:'message_finished'}}}];
    await repository.append(continued,scope,{run_id:'one',events:oldRows});
    const nextRun={...continued,run_id:'two'},nextRows=[...oldRows,{sequence:4,event:{turn_id:'two',update:{type:'message_started',id:'new-run'}}},{sequence:5,event:{turn_id:'two',update:{type:'text_delta',text:'new committed run'}}},{sequence:6,event:{turn_id:'two',update:{type:'message_finished'}}}];
    await repository.append(nextRun,scope,{run_id:'two',events:nextRows});const continuation=await repository.read(nextRun,scope);
    check(continuation.cursor===6&&continuation.messages.some(row=>row.text==='old committed run')&&continuation.messages.some(row=>row.text==='new committed run'),'actual IDB continuation retains cumulative journal under confirmed new run');
    let retiredPage=false;try{await repository.append(nextRun,scope,{run_id:'one',events:oldRows});}catch(error){retiredPage=error.code==='task_run_changed';}
    check(retiredPage&&(await repository.read(nextRun,scope)).cursor===6&&await repository.read(continued,scope)===null,'late old-run same-sequence page cannot replace committed continuation');
    await repository.append(task,scope,{run_id:'one',events:[{sequence:1,event:{update:{type:'message_started',id:'saved'}}},{sequence:2,event:{update:{type:'text_delta',text:'Committed Task content before metadata'}}},{sequence:3,event:{update:{type:'message_finished'}}}]});repository.close();
    pages=createPages({client,host,approvalHost:document.querySelector('#approvals'),chatApprovalHost:document.querySelector('#chat-approvals'),chatVisible:()=>false,locale:()=>language,notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});
    const openedAt=performance.now();await pages.openTask(task);await wait(()=>host.textContent.includes('Committed Task content before metadata')&&getComputedStyle(host.querySelector('.task-history')).visibility==='visible','visible retained Task text');report('retained Task visible after '+Math.round(performance.now()-openedAt)+'ms with measured real DOM');
    check(metadataEntered&&machineEntered,'Task committed transcript mounts while metadata and runtime are still unresolved');
    pages.taskMenu();check(!button('Cancel task'),'retained Task transcript exposes no cancel action');host.querySelector('dialog[open]')?.close();
    await pages.openTask({...task,run_id:'two'});await pause(80);check(!host.textContent.includes('Committed Task content before metadata'),'explicit new Task run cannot borrow the previous run transcript');
    // Usage handler overlap: Resume supersedes the earlier delayed Cancel.
    await pages.openUsage({...scope,name:'Owner',scope:'conversation'});await wait(()=>button('Cancel waiting'),'Usage controls');
    const retainedCancel=button('Cancel waiting');await pages.openUsage({...scope,name:'Owner',scope:'conversation'});check(button('Cancel waiting')===retainedCancel,'Usage reconcile retains the live button while replacing its handler closure');
    holdSnapshot=deferred();holdResume=deferred();button('Cancel waiting').click();await wait(()=>snapshotReads===1,'Usage snapshot');button('Resume').click();await wait(()=>posts.some(row=>row.path==='/api/v1/usage/resume'),'Resume POST');holdSnapshot.resolve();holdSnapshot=null;await pause(100);
    check(!posts.some(row=>row.path==='/api/v1/turn/cancel'),'actual Usage Resume handler fences the older delayed Cancel response before Resume settles or rerenders');holdResume.resolve();holdResume=null;await pause(80);
    await pages.openUsage({...scope,name:'Owner',scope:'conversation'});await wait(()=>button('Cancel waiting')&&!button('Cancel waiting').disabled,'new Usage action');button('Cancel waiting').click();await wait(()=>posts.some(row=>row.path==='/api/v1/turn/cancel'),'valid cancel POST');
    check(posts.filter(row=>row.path==='/api/v1/turn/cancel').length===1,'actual Usage handler posts one legitimate normalized cancel');
    wrongTurn=true;await pages.openUsage({...scope,name:'Owner',scope:'conversation'});await wait(()=>button('Cancel waiting')&&!button('Cancel waiting').disabled,'mismatched cancel');button('Cancel waiting').click();await pause(100);check(posts.filter(row=>row.path==='/api/v1/turn/cancel').length===1,'actual Usage handler rejects a changed active turn');wrongTurn=false;
    language='zh-CN';await pages.openUsage({...scope,name:'Owner',scope:'conversation',returnTask:task});await wait(()=>button('取消等待'),'localized Task usage');button('取消等待').click();await wait(()=>posts.some(row=>row.path==='/api/v1/tasks/t/cancel'),'Task cancel POST');check(snapshotReads===3,'Task returnTask cancellation keeps its separate path without Chat snapshot');
    metadata.resolve();machine.resolve();await pause(100);check(!host.textContent.includes('Committed Task content before metadata'),'late Task metadata/runtime cannot replace newer Usage selection');
    output.textContent+='DONE P1 BROWSER\n';
  }finally{
    metadata.resolve();machine.resolve();holdSnapshot?.resolve();holdResume?.resolve();pages?.dispose();client.resources?.clear();if(session)await session.close();domain?.dispose();cache?.close();
    await Promise.all(names.map(value=>new Promise(resolve=>{const request=indexedDB.deleteDatabase(value);request.onsuccess=request.onerror=resolve;request.onblocked=()=>{output.textContent+='BLOCKED cleanup '+value+'\n';resolve();};})));
  }
}
document.querySelector('#run').onclick=()=>{document.querySelector('#run').disabled=true;run().catch(error=>output.textContent+='FAIL '+error.stack+'\n');};
