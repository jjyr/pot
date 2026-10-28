import assert from 'node:assert/strict';
import {createQueryRegistry} from '../../web/src/state/domains/query.mjs';
import {createDirectoryDomain} from '../../web/src/state/domains/directory.mjs';
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};};
const key=(path,scope={})=>JSON.stringify([path,scope.space||'']);
const caps={version:1,operations:['chat.read','chat.send']};
function fixture(){
  const cached=new Map(),live=new Map(),held=new Map(),states=new WeakMap(),reads=[],commits=new Set(),invalidations=new Set();
  const mark=(value,freshness='fresh')=>{states.set(value,{freshness});return value;};
  const resources={
    query:async(path,scope)=>{const value=cached.get(key(path,scope));return value?{value,freshness:'retained'}:null;},
    read:async(path,scope)=>{const id=key(path,scope);reads.push(id);return held.get(id)?.promise||mark(structuredClone(live.get(id)||{}));},
    state:value=>states.get(value),subscribe:fn=>{commits.add(fn);return()=>commits.delete(fn);},subscribeInvalidation:fn=>{invalidations.add(fn);return()=>invalidations.delete(fn);},
  };
  const spaces=[{id:'one',control_id:'c1',connected:true},{id:'two',control_id:'c2',connected:true}],catalog={items:spaces,active_space_id:'one'};
  const seed=(path,scope,value,retain=true)=>{live.set(key(path,scope),value);if(retain)cached.set(key(path,scope),value);};
  seed('/api/v1/spaces',{},catalog);seed('/api/v1/assistants',{space:'one'},[{id:'a',name:'Cached A',executing:true,capabilities:caps}]);seed('/api/v1/agent-links',{space:'one'},{items:[]});
  seed('/api/v1/assistants',{space:'two'},[{id:'b',name:'Live B',capabilities:caps}],false);seed('/api/v1/agent-links',{space:'two'},{items:[]},false);
  for(const space of spaces)seed('/api/v1/runtime',{space:space.id},{runtime_id:space.control_id,host:{hostname:space.id}},false);
  const client={startup:true,onlineReady:true,runtime:{runtime_id:'c1',boot_id:'boot'},spaces:structuredClone(spaces),connectionGeneration:1,directoryGeneration:-1,directoryAuthorityRevision:0,directorySpaces:new Set(),read:resources.read,scope:()=>({space:'one'}),get operationsReady(){return this.directoryGeneration===this.connectionGeneration;}};
  const queries=createQueryRegistry(resources),domain=createDirectoryDomain(client,{resources,queries}),snapshots=[];
  const hold=(path,scope={})=>{const id=key(path,scope),gate=deferred();held.set(id,gate);return {resolve(value=live.get(id)){if(held.get(id)===gate){held.delete(id);live.set(id,value);}gate.resolve(mark(structuredClone(value)));},reject(error){held.delete(id);gate.reject(error);}};};
  return {domain,client,queries,snapshots,reads,hold,seed,notify:(topics,source)=>{for(const fn of invalidations)fn(topics,source);},watch:()=>domain.watch(value=>{if(value.data)snapshots.push(value.data);})};
}
{
  const f=fixture(),space=f.hold('/api/v1/spaces'),a=f.hold('/api/v1/assistants',{space:'one'}),b=f.hold('/api/v1/assistants',{space:'two'}),links=[f.hold('/api/v1/agent-links',{space:'one'}),f.hold('/api/v1/agent-links',{space:'two'})],machines=[f.hold('/api/v1/runtime',{space:'one'}),f.hold('/api/v1/runtime',{space:'two'})];
  const completion=f.domain.refresh();let finished=false;completion.then(()=>finished=true);const stop=f.watch();let initial;f.domain.query().then(value=>initial=value);await tick();
  assert(initial,'local-only hydration settles initial query while spaces transport is held');assert.deepEqual(initial.assistants.map(item=>item.id),['a']);assert.equal(initial.loading,true);assert.equal(initial.partial,false);assert.equal(initial.assistants[0].executing,false);assert.deepEqual(initial.assistants[0].capabilities.operations,['chat.read']);assert.equal(f.client.operationsReady,false);
  space.resolve();await tick();b.resolve();await tick();assert.deepEqual((await f.domain.query()).assistants.map(item=>item.id),['a','b'],'out-of-order Space completion preserves catalog ordering and cached pending rows');
  a.resolve([{id:'a',name:'Updated A',capabilities:caps}]);await tick();assert.equal((await f.domain.query()).assistants[0].name,'Updated A','held links/runtime do not block assistant update');assert.equal(f.client.operationsReady,false);
  f.seed('/api/v1/assistants',{space:'one'},[{id:'a',name:'Invalidated A',capabilities:caps}]);f.notify(['assistants'],{space:'one'});await tick();assert.equal((await f.domain.query()).assistants[0].name,'Invalidated A','assistant invalidation refreshes independently while links remain held');
  for(const link of links)link.resolve();await tick();assert.equal(f.client.operationsReady,true,'optional runtime metadata cannot delay qualified directory authority');assert.equal(finished,true,'explicit refresh completes independently of optional metadata');assert.equal((await f.domain.query()).loading,false);
  await f.domain.refresh();assert.equal(f.client.operationsReady,true,'a second refresh completes while metadata remains held');for(const machine of machines)machine.resolve();await completion;stop();f.domain.dispose();f.queries.dispose();console.log('PASS local directory first value, independent Space/links/runtime progress, stable ordering and separate live authority');
}
for(const change of ['connection','boot','control']){
  const f=fixture(),a=f.hold('/api/v1/assistants',{space:'one'}),completion=f.domain.refresh(),stop=()=>{};await f.domain.query();await tick();
  if(change==='connection')f.client.connectionGeneration++;if(change==='boot')f.client.runtime.boot_id='next';if(change==='control')f.client.spaces[0].control_id='replacement';
  a.resolve([{id:'a',name:'Must not appear',capabilities:caps}]);await assert.rejects(completion,error=>error.name==='AbortError');assert(!f.client.assistants.some(item=>item.name==='Must not appear'));stop();f.domain.dispose();f.queries.dispose();
}
console.log('PASS retired connection, boot and owning Control cannot publish late directory rows');
{
  const f=fixture(),completion=f.domain.refresh(),stop=f.watch();await completion;const before=f.client.directoryAuthorityRevision,held=f.hold('/api/v1/spaces'),failure=f.domain.refresh();await tick();held.reject(Object.assign(Error('storage'),{storage:true}));await assert.rejects(failure,error=>error.storage);assert.equal(f.client.operationsReady,false);assert.equal(f.client.directoryAuthorityRevision,before+1);assert((await f.domain.query()).assistants.some(item=>item.id==='a'));stop();f.domain.dispose();f.queries.dispose();
}
{
  const f=fixture(),machine=f.hold('/api/v1/runtime',{space:'one'}),completion=f.domain.refresh();await f.domain.query();await tick();assert.equal(f.client.operationsReady,true);await completion;machine.reject(Object.assign(Error('storage'),{storage:true}));await tick();assert.equal(f.client.operationsReady,false,'failure revokes authority granted by this batch');assert((await f.domain.query()).assistants.length);f.domain.dispose();f.queries.dispose();
}
console.log('PASS storage failures retain visible committed rows and revoke both prior and this-batch authority');

{
  const f=fixture(),link=f.hold('/api/v1/agent-links',{space:'two'}),initial=f.domain.refresh();await f.domain.query();await tick();
  const old=f.hold('/api/v1/assistants',{space:'one'});f.notify(['assistants'],{space:'one'});await tick();assert.deepEqual(f.client.assistants.find(item=>item.id==='a').capabilities.operations,['chat.read']);
  link.resolve();await initial;
  const next=f.hold('/api/v1/assistants',{space:'one'}),refresh=f.domain.refresh();await tick();next.resolve([{id:'a',name:'New batch',capabilities:caps}]);await refresh;
  old.resolve([{id:'a',name:'Old batch',capabilities:caps}]);await tick();assert.equal(f.client.assistants.find(item=>item.id==='a').name,'New batch','late replacement read cannot overwrite a later query batch');f.domain.dispose();f.queries.dispose();
}
{
  const f=fixture(),machine=f.hold('/api/v1/runtime',{space:'one'});await f.domain.refresh();await f.domain.refresh();const revision=f.client.directoryAuthorityRevision;machine.reject(Object.assign(Error('old metadata storage'),{storage:true}));await tick();assert.equal(f.client.directoryAuthorityRevision,revision);assert.equal(f.client.operationsReady,true);f.domain.dispose();f.queries.dispose();
}
{
  const f=fixture(),machine=f.hold('/api/v1/runtime',{space:'one'});await f.domain.refresh();const reads=f.reads.length;f.domain.dispose();f.queries.dispose();machine.resolve();await tick();assert.equal(f.reads.length,reads,'disposed metadata cannot launch a follow-up directory read');
}
console.log('PASS replaced dependency batches, newer authority and disposal fence late background completions');
