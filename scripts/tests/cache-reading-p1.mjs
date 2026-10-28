import assert from 'node:assert/strict';
import {captureUsageWait} from '../../web/src/state/usage-wait.mjs';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createResourceReads} from '../../web/src/state/resource-cache.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import {createQueryRegistry,createCommands} from '../../web/src/state/domains/query.mjs';
import {createTasksDomain} from '../../web/src/state/domains/tasks.mjs';
import {createSchedulesDomain} from '../../web/src/state/domains/schedules.mjs';
import {createTaskTranscript,appendTaskEvents} from '../../web/src/state/task-transcript.mjs';
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve};};
const scope={space:'s',owner:'o'},caps=operations=>({version:1,operations});
const originalFetch=globalThis.fetch;
// Real client.snapshot normalizes a UIFrame; request executes the standard
// session/capability fences. No snapshot is committed or session confirmed.
for(const change of ['none','turn','request','owner','control','boot','generation','page','request-generation','session','capability']){
  let posts=0,entered=deferred(),release=deferred(),current=true,requestGeneration=1;
  globalThis.fetch=async(path,options)=>{
    if(path.startsWith('/api/v1/runtime'))return new Response(JSON.stringify({runtime_id:'r',boot_id:'b'}));
    if(options?.method==='POST'){posts++;return new Response('{}');}
    if(path.startsWith('/api/v1/snapshot')){entered.resolve();await release.promise;return new Response(JSON.stringify({kind:'reset',base_cursor:0,view:{state:{boot_id:'b',cursor:1,conversation_id:'c',turn:{user_message_id:change==='turn'?'other':'m'}},messages:[{message:{id:'m',request_id:change==='request'?'other':'waiting',role:'user',text:'synthetic'},sequence:'1',version:1}],replies:[],history:{}}}));}
    throw Error('Unexpected IO '+path);
  };
  const client=await createProductionClient({resourceStorage:createMemoryProjectionStore()});
  client.spaces=[{id:'s',control_id:'r'}];client.assistants=[{id:'o',space_id:'s',capabilities:caps(['chat.cancel'])}];client.setScope(scope);
  const cancel=captureUsageWait(client,scope,{turn:'waiting'},{current:()=>current});
  const pending=cancel('cancel-id',{current:()=>requestGeneration===1});await entered.promise;
  if(change==='owner')client.spaces=[];
  if(change==='control')client.spaces[0].control_id='other';
  if(change==='boot')client.runtime.boot_id='other';
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
  const domain=createTasksDomain({request:async path=>{inflight++;maxInflight=Math.max(inflight,maxInflight);reads++;await Promise.resolve();const after=Number(new URL(path,'http://fixture').searchParams.get('after'));inflight--;return {events:events.slice(after,after+100),has_more:after+100<events.length};}},{resources,queries,commands,transcriptRepository:repository});
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
