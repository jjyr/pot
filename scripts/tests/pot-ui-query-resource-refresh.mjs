import assert from 'node:assert/strict';
import {createQueryRegistry} from '../../web/src/state/domains/query.mjs';
import {createResourceReads} from '../../web/src/state/resource-cache.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';

const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
const path='/api/v1/tasks/catalog';
function fixture(storage=null){
  const calls=[],forces=[],client={runtime:{id:'fixture',boot_id:'boot'},scope:()=>({space:'one',owner:'a'}),request:(path,options)=>new Promise((resolve,reject)=>calls.push({path,options,resolve,reject}))};
  const resources=createResourceReads(client,{storage}),registry=createQueryRegistry(resources);
  const query=registry.define('catalog',async force=>{forces.push(force);return {data:await resources.read(path,{authoritative:force})};},{topics:['tasks','assistants'],space:'one'});
  return {calls,forces,resources,registry,query,close(){registry.dispose();resources.clear();}};
}

// Use both real layers: isolated tests of either scheduler miss the fourth IO.
for(const storage of [null,createMemoryProjectionStore()]){
  const f=fixture(storage),states=[],retired=[];f.query.watch(state=>states.push(state));const stop=f.query.watch(state=>retired.push(state));
  await tick();f.calls[0].resolve({revision:1});await tick();
  const changes=[f.resources.invalidate(['tasks'],{space:'one'})];await tick();
  for(let i=0;i<20;i++)changes.push(f.resources.invalidate(['tasks'],{space:'one'}));
  await tick();assert.equal(f.calls.length,2,'The burst shares one active network read');
  stop();const retiredCount=retired.length;
  f.calls[1].resolve({revision:2});await tick();assert.equal(f.calls.length,3,'Resource fencing makes one follow-up');
  f.calls[2].resolve({revision:3});await tick();
  assert.equal(f.calls.length,3,'The queued event query must reuse the resource read that already satisfied its invalidation');
  await Promise.all(changes);assert.equal(f.query.snapshot.data.revision,3);assert.equal(f.query.pending,false);
  assert.equal(retired.length,retiredCount,'Releasing one consumer does not cancel or republish its shared IO');
  assert.equal(states.at(-1).fresh,true);
  const owners=f.resources.invalidate(['assistants'],{space:'one'});await tick();
  assert.equal(f.calls.length,4,'Assistant changes invalidate the operator catalog and its owner membership');
  f.calls[3].resolve({revision:4});await owners;await tick();assert.equal(f.query.snapshot.data.revision,4);f.close();
}

// Invalidation before the first HTTP starts is covered by that IO, including
// when storage is still obtaining its epoch token.
{
  const storage=createMemoryProjectionStore(),token=storage.token.bind(storage);let entered,release,first=true;
  const waiting=new Promise(resolve=>{entered=resolve;}),gate=new Promise(resolve=>{release=resolve;});
  storage.token=async meta=>{if(first){first=false;entered();await gate;}return token(meta);};
  const f=fixture(storage);f.query.watch(()=>{});await waiting;
  const event=f.resources.invalidate(['tasks'],{space:'one'});await tick();assert.equal(f.calls.length,0);release();await tick();
  f.calls[0].resolve({revision:1});await event;await tick();assert.equal(f.calls.length,1);assert.equal(f.query.snapshot.fresh,true);f.close();
}

// Event revalidation cannot downgrade an explicit force, in either order.
for(const order of [['event','force','force','requery'],['force','event','requery','force']]){
  const f=fixture();f.query.watch(()=>{});await tick();
  const current=f.query.refresh(),waiters=[];let done=false;
  for(const operation of order){
    if(operation==='event')waiters.push(f.resources.invalidate(['tasks'],{space:'one'}));
    else if(operation==='force')waiters.push(f.query.refresh(true));
    else f.query.requery();
  }
  const forced=Promise.all(waiters).then(()=>{done=true;});
  assert.equal(f.query.refresh(),current,'Ordinary readers keep sharing the current query');
  f.calls[0].resolve({revision:1});await tick();
  f.calls[1].resolve({revision:2});await tick();
  assert.equal(f.calls.length,3,'Explicit queued force still does IO after the resource fence is satisfied');
  assert.deepEqual(f.forces,[false,true],'Multiple forces merge with OR across event/requery batching');
  assert.equal(done,false,'Forced waiters cannot resolve with the preceding flight');
  f.query.requery();f.calls[2].resolve({revision:3});await forced;await tick();
  assert.equal(f.query.snapshot.data.revision,3);assert.equal(f.calls.length,3);assert.equal(f.query.pending,false);f.close();
}

// A resource loaded before the event must be reread even if another resource's
// own fence already crossed the event. Reassemble the query, not just its tail.
for(const paginated of [false,true]){
  const f=fixture(),states=[],first=path+'?offset=0',second=path+'?offset=50';
  const q=f.registry.define('combined',async force=>{
    const a=await f.resources.read(first,{authoritative:force});
    const b=!paginated||a.next_offset!==null?await f.resources.read(second,{authoritative:force}):null;
    return {data:{a,b}};
  },{topics:['tasks'],space:'one'});
  q.watch(state=>states.push(state));await tick();f.calls[0].resolve({revision:1,next_offset:50});await tick();
  assert.equal(f.calls[1].path,second);
  const event=f.resources.invalidate(['tasks'],{space:'one'});
  f.calls[1].resolve({revision:1});await tick();assert.equal(f.calls[2].path,second);
  f.calls[2].resolve({revision:2});await tick();assert.equal(f.calls[3].path,first);
  assert(!states.some(state=>state.fresh&&state.data?.a.revision===1&&state.data?.b?.revision===2),'A query crossing an event cannot publish mixed resources as fresh');
  f.calls[3].resolve({revision:2,next_offset:null});await event;await tick();
  assert.equal(f.calls.length,4,'Only the invalidated earlier resource needs more IO during reassembly');
  assert.equal(q.snapshot.data.a.revision,2);assert.equal(q.snapshot.data.b?.revision,paginated?undefined:2);
  assert.equal(q.snapshot.fresh,true);assert.equal(q.pending,false);f.close();
}

// The event can also arrive after all resource IO, before the query publishes.
{
  const f=fixture(),states=[];let loaded,release;
  const read=new Promise(resolve=>{loaded=resolve;}),gate=new Promise(resolve=>{release=resolve;});let first=true;
  const q=f.registry.define('after-io',async force=>{const data=await f.resources.read(path,{authoritative:force});if(first){first=false;loaded();await gate;}return {data};},{topics:['tasks'],space:'one'});
  q.watch(state=>states.push(state));await tick();f.calls[0].resolve({revision:1});await read;
  const event=f.resources.invalidate(['tasks'],{space:'one'});release();await tick();
  assert.equal(f.calls.length,2);assert(!states.some(state=>state.fresh&&state.data?.revision===1));
  f.calls[1].resolve({revision:2});await event;await tick();assert.equal(q.snapshot.data.revision,2);assert.equal(q.snapshot.fresh,true);f.close();
}

// A late event during commit must still fence the response. Explicit refresh
// waits for the accepted projection's commit, and observers cannot see it early.
{
  const storage=createMemoryProjectionStore(),commit=storage.commit.bind(storage);let entered,release;
  const committing=new Promise(resolve=>{entered=resolve;}),gate=new Promise(resolve=>{release=resolve;});
  storage.commit=async(...args)=>{if(args[2].revision===2){entered();await gate;}return commit(...args);};
  const f=fixture(storage),seen=[];f.query.watch(state=>{if(state.data)seen.push(state.data.revision);});
  await tick();f.calls[0].resolve({revision:1});await tick();
  let done=false;const forced=f.query.refresh(true).then(value=>{done=true;return value;});await tick();
  f.calls[1].resolve({revision:2});await committing;
  assert.equal(done,false);assert(!seen.includes(2),'Query subscribers wait for commit');
  const event=f.resources.invalidate(['tasks'],{space:'one'});release();await tick();
  assert.equal(f.calls.length,3,'An event during the last commit still requires IO');
  f.calls[2].resolve({revision:3});assert.equal((await forced).data.revision,3);await event;await tick();
  assert(!seen.includes(2),'An epoch-rejected commit is never published');
  assert.equal((await f.resources.query(path)).value.revision,3);assert.equal(f.calls.length,3);f.close();
}

// A boot reset replaces the entry. An old persistent read cannot transfer its
// satisfied version to the new identity after its commit finishes.
{
  const f=fixture(createMemoryProjectionStore());f.query.watch(()=>{});await tick();
  const event=f.resources.invalidate(['tasks'],{space:'one',boot:'next-boot'});
  f.calls[0].resolve({revision:1});await tick();assert.equal(f.calls.length,2);
  f.calls[1].resolve({revision:2});await event;await tick();
  assert.equal(f.query.snapshot.data.revision,2);assert.equal(f.query.snapshot.fresh,true);
  assert.equal(f.resources.describe(path).boot,'next-boot');assert.equal(f.calls.length,2);f.close();
}

// Failed event reads retain data and recover on the next event, without a loop.
{
  const f=fixture();f.query.watch(()=>{});await tick();f.calls[0].resolve({revision:1});await tick();
  const failed=f.resources.invalidate(['tasks'],{space:'one'});await tick();f.calls[1].reject(Error('offline'));await failed;await tick();
  assert.equal(f.query.snapshot.data.revision,1);assert.equal(f.query.snapshot.status,'error');assert.equal(f.query.pending,false);
  const recovered=f.resources.invalidate(['tasks'],{space:'one'});await tick();f.calls[2].resolve({revision:3});await recovered;await tick();
  assert.equal(f.query.snapshot.data.revision,3);assert.equal(f.query.snapshot.fresh,true);assert.equal(f.calls.length,3);
  await f.resources.invalidate(['tasks'],{space:'other'});assert.equal(f.calls.length,3,'Other scopes do not reread');
  const current=f.query.refresh(true),queued=f.query.refresh(true);f.close();f.calls[3].resolve({revision:4});
  await assert.rejects(current,{name:'AbortError'});await queued;assert.equal(f.calls.length,4,'Disposal settles queued work without starting IO');
}

console.log('Real query/resource refresh integration: 3-read bursts with/without epoch storage, explicit force OR/waiting, shared reads, multi-resource/page consistency, commit fences, scope, disposal and error recovery passed');
