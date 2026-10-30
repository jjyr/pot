import { installWSFixture } from './helpers/ws-fixture.mjs';
installWSFixture();
import assert from 'node:assert/strict';
import {captureUsageWait} from '../../web/src/state/usage-wait.mjs';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createResourceReads} from '../../web/src/state/resource-cache.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import {createQueryRegistry,createCommands} from '../../web/src/state/domains/query.mjs';
import {createDirectoryDomain} from '../../web/src/state/domains/directory.mjs';
import {createTasksDomain} from '../../web/src/state/domains/tasks.mjs';
import {createSchedulesDomain} from '../../web/src/state/domains/schedules.mjs';
import {createTaskTranscript,appendTaskEvents} from '../../web/src/state/task-transcript.mjs';
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve};};
const scope={space:'s',owner:'o'},caps=operations=>({version:1,operations});
const originalFetch=globalThis.fetch;
// Real client.snapshot normalizes a UIFrame; request executes the standard
// session/capability fences. No snapshot is committed or session confirmed.
for(const change of ['none','turn','request','owner','control','boot','source-boot','snapshot-boot','generation','page','request-generation','session','capability']){
  let posts=0,entered=deferred(),release=deferred(),current=true,requestGeneration=1;
  globalThis.fetch=async(path,options)=>{
    if(path.startsWith('/api/v1/runtime'))return new Response(JSON.stringify({runtime_id:'r',boot_id:'b'}));
    if(options?.method==='POST'){posts++;return new Response('{}');}
    if(path.startsWith('/api/v1/snapshot')){entered.resolve();await release.promise;return new Response(JSON.stringify({kind:'reset',base_cursor:0,view:{state:{boot_id:change==='snapshot-boot'?'other':'b',cursor:1,conversation_id:'c',turn:{user_message_id:change==='turn'?'other':'m'}},messages:[{message:{id:'m',request_id:change==='request'?'other':'waiting',role:'user',text:'synthetic'},sequence:'1',version:1}],replies:[],history:{}}}));}
    throw Error('Unexpected IO '+path);
  };
  const client=await createProductionClient({resourceStorage:createMemoryProjectionStore()});
  client.spaces=[{id:'s',control_id:'r'}];client.assistants=[{id:'o',space_id:'s',capabilities:caps(['chat.cancel'])}];client.setScope(scope);
  let sourceBoot='b';const describe=client.resources.describe;client.resources.describe=(...args)=>({...describe(...args),boot:sourceBoot});
  const cancel=captureUsageWait(client,scope,{turn:'waiting'},{current:()=>current});
  const pending=cancel('cancel-id',{current:()=>requestGeneration===1});await entered.promise;
  if(change==='owner')client.spaces=[];
  if(change==='control')client.spaces[0].control_id='other';
  if(change==='boot')client.runtime.boot_id='other';
  if(change==='source-boot')sourceBoot='other';
  if(change==='generation')client.connectionGeneration++;
  if(change==='page')current=false;
  if(change==='request-generation')requestGeneration++;
  if(change==='session')client.sessionReady=()=>false;
  if(change==='capability')client.assistants[0].capabilities=caps([]);
  release.resolve();
  if(change==='none')await pending;else await assert.rejects(pending);
  assert.equal(posts,change==='none'?1:0,change+' POST count');
  client.resources.clear();
}
// A later intent retires an earlier in-flight authority read independently of
// page changes. This models Cancel waiting followed by Resume before either UI
// projection has refreshed.
{
  let generation=0,posts=0;const response=deferred();
  const client={production:true,connectionGeneration:0,runtime:{runtime_id:'r',boot_id:'b'},spaces:[{id:'s',control_id:'r'}],snapshot:()=>response.promise,request:async()=>{posts++;}};
  const cancel=captureUsageWait(client,scope,{turn:'wait'}),ownGeneration=++generation;
  const pending=cancel('old',{current:()=>ownGeneration===generation});
  ++generation;response.resolve({owner_id:JSON.stringify(['s','o']),turn:{user_message_id:'m'},messages:[{id:'m',request_id:'wait'}]});
  await assert.rejects(pending);assert.equal(posts,0);
}
globalThis.fetch=originalFetch;
console.log('PASS actual production Usage snapshot/cancel: valid POST; turn/request/identity/boot/generation/page/session/capability changes reject');

for(const fault of ['register','invalidate','retirement']){
  const calls=[],storage={
    async read(){calls.push('read');return {value:{items:[{id:'committed'}]},lastConfirmed:1,fresh:true};},
    async register(){calls.push('register');if(fault==='register')throw Object.assign(Error('write fault'),{storage:true});},
    async invalidate(){calls.push('invalidate');throw Object.assign(Error('epoch fault'),{storage:true});},
    subscribe:()=>()=>{},close(){},
  };
  const client={production:true,onlineReady:false,runtime:{runtime_id:'r',boot_id:'b'},scope:()=>scope,spaces:[{id:'s',control_id:'r'}],transport:async()=>{throw Error('No transport permitted');}};
  const reads=createResourceReads(client,{storage});
  if(fault!=='register')await reads.invalidate(fault==='retirement'?['spaces']:['tasks'],{space:'s'});
  if(fault==='retirement')await assert.rejects(reads.read('/api/v1/tasks/t'),error=>error.storage);
  else {const value=await reads.read('/api/v1/tasks/t');assert.equal(value.items[0].id,'committed');assert.notEqual(reads.state(value).freshness,'fresh');}
  assert.ok(calls.includes('read'),'read committed despite write fault');
  await assert.rejects(reads.read('/api/v1/tasks/t',{authoritative:true}),error=>error.storage);
  reads.clear();
}
console.log('PASS registration and pending epoch faults retain legal data, authoritative refresh and destructive retirement stay fenced');

// Known display identities enumerate cached schedules without resurrecting manage.
{
  const calls=[],resources={state:()=>({freshness:'retained'}),query:async(path,options)=>{calls.push(options);return {value:{items:[{id:'cached-schedule'}]}};},subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const client={runtime:{runtime_id:'r'},spaces:[{id:'s',control_id:'r'}],assistants:[{id:'o',space_id:'s',capabilities:caps(['chat.read'])},{id:'unknown',space_id:'absent',capabilities:caps(['chat.read'])}],operationScopeReady:()=>false};
  const queries=createQueryRegistry(resources),commands=createCommands(client),domain=createSchedulesDomain(client,{resources,queries,commands});
  const values=[];const stop=domain.watchList(value=>values.push(value));await tick();
  assert.deepEqual(calls.map(value=>[value.space,value.owner]),[['s','o']]);assert.equal(values.at(-1).data[0].value.items[0].id,'cached-schedule');assert.equal(values.at(-1).fresh,false);assert.deepEqual(client.assistants[0].capabilities.operations,['chat.read']);
  await assert.rejects(domain.run(scope,'cached-schedule'));assert.throws(()=>domain.preview(scope,{}));
  stop();queries.dispose();commands.dispose();
}
console.log('PASS schedules query only known scoped retained identities without manage restoration');

// Real query scheduling, actual transcript reducer, synthetic committed repository.
for(const mode of ['complete','stop','storage-failure','stale-metadata']){
  const events=Array.from({length:6203},(_,i)=>({sequence:i+1,event:{update:{type:'input_applied',id:'i'+i}}}));
  const state=createTaskTranscript(),updates=[],batches=[];let reads=0,inflight=0,maxInflight=0,stopped=false,stop;
  const record={run_id:'run',capabilities:caps(['task.events'])};
  const resources={read:async()=>record,state:()=>({freshness:mode==='stale-metadata'?'retained':'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands({});
  const repository={read:async()=>state,append:async(_,__,page)=>{if(mode==='storage-failure'&&state.cursor===5000)throw Object.assign(Error('commit failed'),{storage:true});appendTaskEvents(state,page.events);return state;},close(){}};
  const domain=createTasksDomain({request:async path=>{inflight++;maxInflight=Math.max(inflight,maxInflight);reads++;await Promise.resolve();const after=Number(new URL(path,'http://fixture').searchParams.get('after'));inflight--;return {run_id:'run',events:events.slice(after,after+100),has_more:after+100<events.length};}},{resources,queries,commands,transcriptRepository:repository});
  stop=domain.watchDetail({id:'t',run_id:'run'},scope,value=>{updates.push({cursor:value.data?.state.cursor,fresh:value.fresh,catchingUp:value.data?.catchingUp});if(value.data?.catchingUp){batches.push(reads);if(mode==='stop'&&!stopped){stopped=true;stop();}}});
  for(let i=0;i<50;i++){await tick();if(mode==='complete'&&updates.at(-1)?.fresh||mode==='storage-failure'&&reads===51||mode==='stop'&&stopped||mode==='stale-metadata')break;}
  assert.equal(maxInflight,mode==='stale-metadata'?0:1);
  if(mode==='complete'){assert.equal(state.cursor,6203);assert.equal(reads,63);assert.deepEqual(batches,[50]);assert.ok(updates.some(value=>value.cursor===5000&&!value.fresh&&value.catchingUp));assert.equal(updates.at(-1).fresh,true);}
  if(mode==='stop'){await tick();assert.equal(reads,50);assert.equal(state.cursor,5000);}
  if(mode==='storage-failure'){assert.equal(state.cursor,5000);assert.equal(updates.at(-1).fresh,false);await tick();assert.equal(reads,51);}
  if(mode==='stale-metadata'){assert.equal(reads,0);assert.equal(updates.at(-1).fresh,false);}
  stop();queries.dispose();domain.dispose();commands.dispose();
}
console.log('PASS static 6203-event journal auto-catches up in bounded serial batches; stop, commit failure and stale metadata cannot publish fresh or start extra IO');

// Metadata provenance, rather than a view's attachment time, grants action IO.
for(const change of ['connection','serving-boot','source-boot','control']){
  const response=deferred();let sourceBoot='remote-one',eventReads=0,appends=0;
  const client={connectionGeneration:1,runtime:{runtime_id:'serving',boot_id:'serving-one'},spaces:[{id:'s',control_id:'remote'}],operationScopeReady:()=>true,request:async()=>{eventReads++;return {run_id:'run',events:[],has_more:false};}};
  const resources={describe:()=>({control:client.spaces[0].control_id,boot:sourceBoot}),read:()=>response.promise,state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands(client),domain=createTasksDomain(client,{resources,queries,commands,transcriptRepository:{read:async()=>null,append:async()=>{appends++;},close(){}}});
  const pending=domain.detail({id:'t',run_id:'run'},scope).value();
  if(change==='connection')client.connectionGeneration++;
  if(change==='serving-boot')client.runtime.boot_id='serving-two';
  if(change==='source-boot')sourceBoot='remote-two';
  if(change==='control')client.spaces[0].control_id='replacement';
  response.resolve({run_id:'run',capabilities:caps(['task.events'])});await assert.rejects(pending,{name:'AbortError'});assert.equal(eventReads,0);assert.equal(appends,0);
  domain.dispose();queries.dispose();commands.dispose();
}
{
  const reads=[],observedRuns=[],client={runtime:{runtime_id:'r',boot_id:'b'},spaces:[{id:'s',control_id:'r'}],request:async()=>({run_id:'new-run',events:[],has_more:false})};
  const resources={read:async(_,options)=>{reads.push(options.authoritative);return {run_id:options.authoritative===true?'new-run':'old-run',capabilities:caps(['task.events'])};},state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands(client),domain=createTasksDomain(client,{resources,queries,commands,transcriptRepository:{read:async task=>{observedRuns.push(task.run_id);return null;},append:async()=>createTaskTranscript(),close(){}}});
  const result=await domain.detail({id:'t',run_id:'new-run'},scope).value();assert.deepEqual(reads,['invalidated',true]);assert.deepEqual(observedRuns,['new-run']);assert.equal(result.record.run_id,'new-run');
  resources.read=async()=>({run_id:'old-run',capabilities:caps(['task.events'])});const mismatched=await domain.detail({id:'t',run_id:'unknown-run'},scope).value();assert.equal(mismatched.fresh,false);assert.ok(mismatched.connectionError);assert.deepEqual(observedRuns,['new-run','old-run']);
  domain.dispose();queries.dispose();commands.dispose();
}
{
  const client={connectionGeneration:0,runtime:{runtime_id:'r',boot_id:'b'},spaces:[{id:'s',control_id:'r'}],assistants:[{id:'o',space_id:'s',capabilities:caps(['schedules.manage'])}],request:async()=>{throw Error('Wrong-identity POST');}};
  const resources={describe:()=>({control:'r',boot:'source'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands(client),domain=createTasksDomain(client,{resources,queries,commands});
  const pending=domain.cancel({id:'t',run_id:'run'},scope);client.connectionGeneration++;await assert.rejects(pending,error=>error.uiKey==='identityChanged');
  const schedules=createSchedulesDomain(client,{resources,queries,commands}),schedule=schedules.run(scope,'s');client.connectionGeneration++;await assert.rejects(schedule,error=>error.uiKey==='identityChanged');
  domain.dispose();queries.dispose();commands.dispose();
}
console.log('PASS metadata and command IO fence serving/source boot, Control and connection changes; known new runs require matching authoritative metadata');

// A Task journal has cumulative sequence numbers across authoritative run changes.
{
  let run='one',revision=1,eventRun='one',appends=0;const reads=[],states=new Map(),updates=[];
  const journal=[{sequence:1,event:{update:{type:'message_started',id:'old'}}},{sequence:2,event:{update:{type:'text_delta',text:'old run'}}},{sequence:3,event:{update:{type:'message_finished'}}},{sequence:4,event:{update:{type:'message_started',id:'new'}}},{sequence:5,event:{update:{type:'text_delta',text:'new run'}}},{sequence:6,event:{update:{type:'message_finished'}}}];
  const client={runtime:{runtime_id:'r',boot_id:'b'},spaces:[{id:'s',control_id:'r'}],request:async path=>{const after=Number(new URL(path,'http://fixture').searchParams.get('after'));return {run_id:eventRun,events:journal.slice(after,eventRun==='one'?3:6),has_more:false};}};
  const resources={read:async(_,options)=>{reads.push(options.authoritative);return {run_id:run,revision,capabilities:caps(['task.events'])};},state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands(client),repository={read:async task=>states.get(task.run_id)||null,append:async(task,_,page,{validate})=>{validate();appends++;const state=states.get(task.run_id)||createTaskTranscript();appendTaskEvents(state,page.events);states.set(task.run_id,state);return state;},close(){}};
  const domain=createTasksDomain(client,{resources,queries,commands,transcriptRepository:repository}),task={id:'t',run_id:'one'},query=domain.detail(task,scope);const stop=query.watch(value=>updates.push({record:value.data?.record.run_id,fresh:value.fresh}));
  await query.value();assert.equal(query.snapshot.data.state.cursor,3);
  run=eventRun='two';revision=2;await query.refresh(true);
  assert.equal(query.snapshot.data.record.run_id,'two');assert.equal(query.snapshot.data.state.cursor,6);assert.ok(query.snapshot.data.state.messages.some(row=>row.text==='old run'));assert.ok(query.snapshot.data.state.messages.some(row=>row.text==='new run'));assert.equal(query.snapshot.fresh,true);assert.ok(reads.filter(value=>value===true).length>=2);
  const before=appends;eventRun='one';await query.refresh(true);assert.equal(appends,before,'late old-run page performs zero append');assert.equal(query.snapshot.fresh,false);assert.equal(query.snapshot.data.state.cursor,6);
  eventRun='two';await query.refresh(true);assert.equal(query.snapshot.fresh,true);
  stop();domain.dispose();queries.dispose();commands.dispose();
}
for(const returnedRun of [undefined,'old-run']){
  let appends=0;const record={run_id:'new-run',revision:2,capabilities:caps(['task.events'])},resources={read:async()=>record,state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const client={request:async()=>({run_id:returnedRun,events:[{sequence:1,event:{update:{type:'text_delta',text:'wrong source'}}}],has_more:false})},queries=createQueryRegistry(resources),commands=createCommands(client),domain=createTasksDomain(client,{resources,queries,commands,transcriptRepository:{read:async()=>null,append:async()=>{appends++;return createTaskTranscript();},close(){}}});
  const value=await domain.detail({id:'t',run_id:'new-run'},scope).value();assert.equal(appends,0);assert.equal(value.fresh,false);assert.ok(value.connectionError);
  domain.dispose();queries.dispose();commands.dispose();
}
console.log('PASS authoritative same-id continuation rebuilds cumulative journal; old/missing run and reused sequences perform zero append and never become fresh');

// Metadata from a retired run cannot supersede a newer revision, nor can a
// command queued by an old view dispatch after that view's run has changed.
{
  const oldMetadata=deferred();let reads=0,appends=0,valid=true;
  const client={runtime:{runtime_id:'r',boot_id:'b'},spaces:[{id:'s',control_id:'r'}],request:async()=>({run_id:'two',events:[],has_more:false})};
  const resources={read:async()=>++reads===1?oldMetadata.promise:{run_id:'two',revision:2,capabilities:caps(['task.events'])},state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands(client),domain=createTasksDomain(client,{resources,queries,commands,transcriptRepository:{read:async()=>null,append:async()=>{appends++;return createTaskTranscript();},close(){}}});
  const old=domain.detail({id:'t',run_id:'one'},scope).value();await domain.detail({id:'t',run_id:'two'},scope).value();const before=appends;
  oldMetadata.resolve({run_id:'one',revision:1,capabilities:caps(['task.events'])});await assert.rejects(old,{name:'AbortError'});assert.equal(appends,before);
  const pending=domain.cancel({id:'t',run_id:'two',_valid:()=>valid},scope);valid=false;await assert.rejects(pending,error=>error.uiKey==='identityChanged');
  domain.dispose();queries.dispose();commands.dispose();
}
console.log('PASS late lower-revision Task metadata and a retired view command cannot append or dispatch');

// A metadata-to-events race must not relabel an old page with a newly confirmed run.
{
  const oldPage=deferred(),newPage=deferred(),oldEntered=deferred(),newEntered=deferred();let run='one',revision=1,appends=0;
  const client={runtime:{runtime_id:'r',boot_id:'b'},spaces:[{id:'s',control_id:'r'}],request:async(_,options)=>{assert.equal(options.expectedControl,'r');if(run==='one'){oldEntered.resolve();return oldPage.promise;}newEntered.resolve();return newPage.promise;}};
  const resources={describe:()=>({control:'r',boot:'b'}),read:async()=>({run_id:run,revision,capabilities:caps(['task.events'])}),state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands(client),domain=createTasksDomain(client,{resources,queries,commands,transcriptRepository:{read:async()=>null,append:async(task,_,page,{validate})=>{validate();appends++;const state=createTaskTranscript();appendTaskEvents(state,page.events);return state;},close(){}}});
  const first=domain.detail({id:'t',run_id:'one'},scope),second=domain.detail({id:'t',run_id:'two'},scope),pending=first.value();await oldEntered.promise;
  run='two';revision=2;const next=second.value();await newEntered.promise;
  assert.equal(second.snapshot.data.record.run_id,'two');assert.equal(second.snapshot.data.state.cursor,0);assert.equal(second.snapshot.fresh,false,'confirmed new window is retained while events wait');
  oldPage.resolve({run_id:'one',events:[{sequence:1,event:{update:{type:'message_started',id:'old'}}}],has_more:false});await assert.rejects(pending,{name:'AbortError'});assert.equal(appends,0);
  newPage.resolve({run_id:'two',events:[{sequence:1,event:{update:{type:'message_started',id:'new'}}}],has_more:false});await next;assert.equal(appends,1);assert.equal(second.snapshot.fresh,true);
  domain.dispose();queries.dispose();commands.dispose();
}
// Failure to confirm continuation leaves the previous committed run readable and stale.
{
  let run='one',freshness='fresh',reads=0;const state=createTaskTranscript();appendTaskEvents(state,[{sequence:1,event:{update:{type:'message_started',id:'retained'}}}]);
  const client={request:async()=>{reads++;return {run_id:run,events:[],has_more:false};}},resources={read:async()=>({run_id:run,revision:run==='one'?1:2,capabilities:caps(['task.events'])}),state:()=>({freshness}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands(client),domain=createTasksDomain(client,{resources,queries,commands,transcriptRepository:{read:async()=>state,append:async()=>state,close(){}}}),query=domain.detail({id:'t',run_id:'one'},scope);
  await query.value();run='two';freshness='retained';await assert.rejects(query.refresh(true));assert.equal(query.snapshot.data.record.run_id,'one');assert.equal(query.snapshot.data.state,state);assert.equal(query.snapshot.fresh,false);assert.equal(reads,1);
  domain.dispose();queries.dispose();commands.dispose();
}
console.log('PASS metadata→events run changes retire old pending pages and expose only the new retained window; unconfirmed continuation preserves old read-only output');

// Directory display retention and dispatcher authority share one qualification.
{
  let fault=false,freshness='fresh';const client={runtime:{runtime_id:'r',boot_id:'b'},connectionGeneration:1,directoryGeneration:1,spaces:[{id:'s',control_id:'r'}],get operationsReady(){return this.directoryGeneration===this.connectionGeneration;}};
  const resources={read:async path=>{if(path==='/api/v1/spaces')return {items:[{id:'s',control_id:'r',connected:true}]};if(path==='/api/v1/assistants'){if(fault)throw Object.assign(Error('synthetic storage'),{storage:true});return [{id:'o',capabilities:caps(['chat.send'])}];}return {items:[]};},state:()=>({freshness}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands(client),domain=createDirectoryDomain(client,{resources,queries,commands});
  const committed=await domain.refresh();const owners=client.assistants;fault=true;await assert.rejects(domain.refresh(),error=>error.storage);assert.equal(client.operationsReady,false);assert.equal(await domain.query(),committed.data);assert.equal(client.assistants,owners);assert.equal(client.assistants[0].offline,false);
  fault=false;freshness='retained';await domain.refresh();assert.equal(client.operationsReady,false,'retained retry cannot restore command authority');freshness='fresh';await domain.refresh();assert.equal(client.operationsReady,true);
  queries.dispose();commands.dispose();
}
for(const changed of ['confirmation','connection','boot','control']){
  const blocked=deferred(),entered=deferred(),client={runtime:{runtime_id:'r',boot_id:'b'},connectionGeneration:1,directoryGeneration:1,directoryAuthorityRevision:1,spaces:[{id:'s',control_id:'r'}]};
  const resources={read:async path=>{if(path==='/api/v1/spaces')return {items:[{id:'s',control_id:'r',connected:true}]};if(path==='/api/v1/assistants'){entered.resolve();return blocked.promise;}return {};},state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}};
  const queries=createQueryRegistry(resources),commands=createCommands(client),domain=createDirectoryDomain(client,{resources,queries,commands}),pending=domain.refresh();await entered.promise;
  if(changed==='confirmation')client.directoryAuthorityRevision++;if(changed==='connection')client.connectionGeneration++;if(changed==='boot')client.runtime.boot_id='new';if(changed==='control')client.spaces[0].control_id='new';
  blocked.resolve(Promise.reject(Object.assign(Error('old failure'),{storage:true})));await assert.rejects(pending);assert.equal(client.directoryGeneration,1,'old failure cannot revoke '+changed);
  queries.dispose();commands.dispose();
}
console.log('PASS directory storage faults retire shared dispatch authority, retained Retry cannot restore it, and retired failures cannot revoke newer confirmation/connection/boot/Control');
