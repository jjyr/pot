import assert from 'node:assert/strict';
import {createQueryRegistry,createCommands} from '../../web/src/state/domains/query.mjs';
import {createProvidersDomain} from '../../web/src/state/domains/providers.mjs';
import {createDirectoryDomain} from '../../web/src/state/domains/directory.mjs';
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});return {resolve,reject,promise};};
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
let committed,loads=0;const changes=new Set(),invalidations=new Set();
const resources={state:()=>({freshness:'fresh'}),subscribe(fn){changes.add(fn);return()=>changes.delete(fn);},subscribeInvalidation(fn){invalidations.add(fn);return()=>invalidations.delete(fn);}};
const registry=createQueryRegistry(resources),io=deferred();
const query=registry.define('tasks:one',async()=>{loads++;const data=await io.promise;assert.equal(committed,data,'Queries publish only committed data');return {data};},{topics:['tasks'],space:'one'});
const a=[],b=[],stopA=query.watch(state=>a.push(state)),stopB=query.watch(state=>b.push(state));assert.equal(loads,1,'Two views share their first read');stopA();registry.notify(['tasks'],{space:'one'});committed={items:[1]};io.resolve(committed);await tick();assert.equal(a.at(-1).status,'loading');assert.equal(b.at(-1).data,committed,'Disposing one view leaves shared IO active');assert.equal(b.at(-1).fresh,true);
await registry.notify(['tasks'],{space:'other'});assert.equal(loads,2,'Invalidations during shared IO receive one follow-up; another node does not refresh');stopB();registry.dispose();assert.equal(changes.size,0);assert.equal(invalidations.size,0);
const bounded=async promise=>{let timer;try{return await Promise.race([promise,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('Query batch did not settle')),1000);})]);}finally{clearTimeout(timer);}};
for(const order of [['force','requery','force','read'],['requery','read','force','requery'],['force','force'],['requery','requery']]){
  const registry=createQueryRegistry(resources),gates=[],forces=[];let active=0,maxActive=0;
  const q=registry.define('interleaved',async force=>{forces.push(force);active++;maxActive=Math.max(maxActive,active);const gate=deferred();gates.push(gate);try{return {data:await gate.promise};}finally{active--;}});
  const first=q.refresh(),waiters=[];let forcedDone=false;
  for(const operation of order){if(operation==='force')waiters.push(q.refresh(true));else if(operation==='requery')q.requery();else assert.equal(q.refresh(),first,'Ordinary readers share the current batch');}
  const forced=Promise.all(waiters).then(value=>{forcedDone=true;return value;});
  gates[0].resolve('initial');assert.equal((await bounded(first)).data,'initial');await tick();
  assert.deepEqual(forces,[false,order.includes('force')]);
  if(waiters.length)assert.equal(forcedDone,false,'Forced waiters must not resolve with the old flight');
  // A commit during the queued read needs one later local query. It cannot make
  // that queued read wait for itself or for every future commit.
  q.requery();q.requery();gates[1].resolve('confirmed');
  const results=await bounded(forced);for(const result of results)assert.equal(result.data,'confirmed');await tick();
  assert.deepEqual(forces,[false,order.includes('force'),false]);
  gates[2].resolve('committed');await tick();assert.equal(q.snapshot.data,'committed');assert.equal(q.pending,false);assert.equal(maxActive,1);assert.equal(forces.length,3,'No self-triggered refresh loop');registry.dispose();
}
for(const failingBatch of [0,1]){
  const registry=createQueryRegistry(resources),gates=[],forces=[];
  const q=registry.define('failure',async force=>{forces.push(force);const gate=deferred();gates.push(gate);return {data:await gate.promise};});
  const first=q.refresh(),forced=q.value(true),firstResult=assert[failingBatch===0?'rejects':'doesNotReject'](first),forcedResult=failingBatch===1?assert.rejects(forced,/batch failed/):forced;
  if(failingBatch===0)gates[0].reject(Error('batch failed'));else gates[0].resolve('old');
  await bounded(firstResult);await tick();assert.deepEqual(forces,[false,true]);
  if(failingBatch===1)gates[1].reject(Error('batch failed'));else gates[1].resolve('recovered');
  const result=await bounded(forcedResult);if(failingBatch===0)assert.equal(result,'recovered');
  await tick();assert.equal(q.pending,false);assert.equal(forces.length,2);registry.dispose();
}
{
  const registry=createQueryRegistry(resources),forces=[];let q,forced;
  q=registry.define('reentrant',async force=>{forces.push(force);if(!force)forced=q.refresh(true);return {data:force?'confirmed':'initial'};});
  await bounded(q.refresh());assert.equal((await bounded(forced)).data,'confirmed');await tick();assert.deepEqual(forces,[false,true]);assert.equal(q.pending,false);registry.dispose();
}
{
  const registry=createQueryRegistry(resources),gate=deferred(),forces=[];
  const q=registry.define('disposed',async force=>{forces.push(force);await gate.promise;return {data:'committed'};});
  const first=q.refresh(),forced=q.refresh(true);q.requery();registry.dispose();gate.resolve();
  await bounded(Promise.all([first,forced]));assert.deepEqual(forces,[false],'Disposal settles queued waiters without starting another read');assert.equal(q.pending,false);
}
const commands=createCommands({}),scope={space:'one',owner:'assistant'},pending=deferred();let sends=0,identity;
const send=(body,id)=>{sends++;identity=id;assert.deepEqual(body,{text:'intent'});return pending.promise;};
const first=commands.run(scope,'task:t:input',{text:'intent'},send,{idempotent:true}),second=commands.run({...scope},'task:t:input',{text:'intent'},send,{idempotent:true});assert.equal(first,second);await tick();assert.equal(sends,1);assert.equal(commands.pending(scope,'task:'),true);assert.equal(commands.pending({...scope,owner:'other'},'task:'),false);pending.reject(Object.assign(Error('local commit failed'),{accepted:true}));await assert.rejects(first,/local commit/);let retriedID;await commands.run(scope,'task:t:input',{text:'intent'},(_,id)=>{retriedID=id;return {accepted:true};},{idempotent:true});assert.equal(retriedID,identity,'Manual retry retains the accepted intent identity');
const reads=[];const domain=createProvidersDomain({request:async()=>({})},{resources:{read:async(path,options)=>{reads.push({path,options});return {profiles:[]};},state:()=>({freshness:'fresh'})},queries:createQueryRegistry(resources),commands});
assert.equal(domain.catalog({space:'one',owner:'a',signal:new AbortController().signal}),domain.catalog({space:'one',owner:'b',request_id:'editor'}),'Provider catalog normalizes shared scope and excludes consumer context');await domain.query({space:'one',owner:'a'});assert.deepEqual(reads[0].options,{space:'one',owner:'',authoritative:false});commands.dispose();
for(const path of ['/api/v1/assistants','/api/v1/agent-links','/api/v1/runtime']){
  let failing=true,offline=false;const error=Object.assign(Error('quota failed'),{storage:true});
  const read=async target=>{if(target===path&&failing)throw offline?Error('network unavailable'):error;if(target==='/api/v1/spaces')return {items:[{id:'one',connected:true,assistants:[{id:'embedded',executing:true}]}]};if(target==='/api/v1/assistants')return [{id:'a',executing:true}];if(target==='/api/v1/agent-links')return {items:[]};return {};};
  const registry=createQueryRegistry(resources),client={read},directory=createDirectoryDomain(client,{resources:{...resources,read},queries:registry});
  await assert.rejects(directory.query(),value=>value===error,'Storage failure cannot become a successful partial/offline directory');
  failing=false;const committed=(await directory.refresh()).data;assert.equal(committed.assistants[0].offline,false);
  failing=true;await assert.rejects(directory.refresh(),value=>value===error);assert.equal(await directory.query(),committed,'Failed refresh retains the last committed directory');assert.equal(client.assistants[0].offline,false,'Storage failure cannot relabel a connected object as offline');
  offline=true;const partial=await directory.refresh();assert.equal(partial.error,null,'Real network failure can retain offline presentation');
  failing=false;assert.equal((await directory.refresh()).data.partial,false,'Explicit retry recovers');registry.dispose();
}
console.log('Domain queries: shared first read, consumer disposal, committed publication, freshness, scope isolation, force/requery batching and waiting, failure recovery, reentrant ownership; commands: shared intent and retry identity; Provider scope normalization passed');
