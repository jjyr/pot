import assert from 'node:assert/strict';
import {createResourceCache,createResourceReads} from '../../web/src/state/resource-cache.mjs';
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};};
const tick=async()=>{for(let i=0;i<10;i++)await Promise.resolve();};
let time=0,reads=0;const notices=[],cache=createResourceCache({now:()=>time,onChange:(key,error)=>notices.push({key,error})});
let pending=deferred();const load=()=>{reads++;return pending.promise;};
const released=new AbortController(),a=cache.read('control/a',load,{signal:released.signal}),b=cache.read('control/a',load);
assert.equal(reads,1,'Concurrent consumers share IO');released.abort();await assert.rejects(a,{name:'AbortError'});
pending.resolve({name:'first'});assert.deepEqual(await b,{name:'first'},'One consumer leaving cannot abort another');
const first=await cache.read('control/a',load);first.name='mutated';assert.equal(cache.peek('control/a').name,'first','Consumers cannot mutate another view’s snapshot');
assert.equal(reads,1,'Fresh visits need no read');assert.equal(notices.length,0,'Awaited first loads do not trigger a second consumer refresh or reset pagination');
const isolated=cache.read('control/b',async()=>({name:'other'}));assert.equal((await isolated).name,'other');
time=31000;pending=deferred();assert.equal((await cache.read('control/a',load)).name,'first','Stale content is available without waiting');assert.equal(reads,2);
cache.invalidate(key=>key==='control/a');pending.resolve({name:'obsolete'});pending=deferred();await tick();assert.equal(reads,3,'Invalidation during IO requires a follow-up');assert.equal(cache.peek('control/a').name,'first','An invalidated response is never published');
pending.resolve({name:'current'});await tick();assert.equal(cache.peek('control/a').name,'current');
time=62000;pending=deferred();await cache.read('control/a',load);pending.reject(Error('Offline'));await tick();assert.equal(cache.peek('control/a').name,'current','Background failure retains content');assert.equal(notices.at(-1).error.message,'Offline');
cache.set('control/a',{name:'saved'});assert.equal((await cache.read('control/a',load)).name,'saved','Write-through immediately supplies saved data');
cache.invalidate(key=>key==='control/b');let related=0;assert.equal((await cache.read('control/b',async()=>{related++;return {name:'related'};})).name,'other');await tick();assert.equal(related,1);
cache.reset();pending=deferred();const old=cache.read('control/a',load);cache.reset();await cache.read('control/a',async()=>({name:'new boot'}));pending.resolve({name:'old boot'});await assert.rejects(old,{name:'AbortError'});assert.equal(cache.peek('control/a').name,'new boot','An old boot finishing late cannot replace the new boot');

const calls=[];let scope={space:'a',owner:'one'};
const client={runtime:{runtime_id:'runtime',boot_id:'boot'},spaces:[{id:'a',control_id:'control-a'},{id:'b',control_id:'control-b'}],scope:()=>scope,request:async(path,options)=>{calls.push({path,...options});return {path,space:options.space};}};
const resources=createResourceReads(client);
await resources.read('/api/v1/providers');scope.owner='two';await resources.read('/api/v1/providers');assert.equal(calls.length,1,'Provider reads share one Space cache across assistant contexts');
scope.space='b';await resources.read('/api/v1/providers');assert.equal(calls.length,2,'Spaces and Controls cannot share values');
await resources.read('/api/v1/permissions');await resources.read('/api/v1/permissions');assert.equal(calls.length,4,'Approval and permission authority is never cached');
await resources.read('/api/v1/agent');await resources.read('/api/v1/agent');assert.equal(calls.length,6,'Editor revisions are always authoritative');
await resources.read('/api/v1/usage/requests?offset=0');await resources.read('/api/v1/usage/requests?offset=20');assert.equal(calls.length,8,'Pagination is part of resource identity');
resources.invalidate(['providers'],{space:'a'});await resources.read('/api/v1/providers');assert.equal(calls.length,8,'Invalidating another Space leaves this one fresh');
resources.invalidate([],{space:'b',boot:'new-boot'});await resources.read('/api/v1/providers');assert.equal(calls.length,9,'A Control boot cannot reuse previous data');
resources.invalidate([],{space:'b',reset:true});await resources.read('/api/v1/providers');assert.equal(calls.length,10,'Reconnect reset reconciles current authority');
const delayed=deferred();const scopedClient={...client,request:async(path,options)=>options.space==='a'?delayed.promise:{space:options.space}};
const scopedReads=createResourceReads(scopedClient);scope.space='a';const oldSpace=scopedReads.read('/api/v1/providers');scope.space='b';assert.equal((await scopedReads.read('/api/v1/providers')).space,'b');delayed.resolve({space:'a'});assert.equal((await oldSpace).space,'a');assert.equal((await scopedReads.read('/api/v1/providers')).space,'b','A late response stays in its captured Space');
let collections=0;
const collection=()=>{collections++;return Promise.resolve({items:Array.from({length:5000},(_,id)=>({id}))});};
const collectionPath='/api/v1/tasks/query?scope=active&limit=50';
await Promise.all([scopedReads.collection(collectionPath,{},collection),scopedReads.collection(collectionPath,{},collection)]);
await scopedReads.collection(collectionPath,{},collection);assert.equal(collections,1,'Paginated sources share one complete collection independently of backend page count');
console.log('Resource cache: sharing, isolation, stale reads, invalidation races, consumer release, writes, failures and boot/reset passed');

// Exercise the real shared transport's command hook, including a scope switch
// while a save is pending. Commands invalidate their captured Space, not the UI's
// newly selected Space, and never place request credentials into a read cache.
const {createProductionClient}=await import('../../web/src/adapters/production.mjs');
const originalFetch=globalThis.fetch,transportCalls=[];let save=deferred(),version=1;
globalThis.fetch=async(path,options={})=>{transportCalls.push({path,method:options.method});const url=new URL(path,'http://fixture');if(options.method==='POST'){await save.promise;version++;return new Response(JSON.stringify({saved:true}));}return new Response(JSON.stringify(url.pathname==='/api/v1/runtime'?{runtime_id:'transport',boot_id:'boot'}:{version}));};
try{
  const transport=await createProductionClient();transport.spaces=[{id:'a',control_id:'a'},{id:'b',control_id:'b'}];
  transport.setScope({space:'a',owner:'alpha'});await transport.read('/api/v1/providers');await transport.read('/api/v1/agent/routing');
  transport.setScope({space:'b',owner:'beta'});await transport.read('/api/v1/providers');transport.setScope({space:'a',owner:'alpha'});
  const writing=transport.request('/api/v1/providers',{method:'POST',body:{api_key:'fixture-private-key'}});transport.setScope({space:'b',owner:'beta'});save.resolve();await writing;
  const beforeReads=transportCalls.length;await transport.read('/api/v1/providers');assert.equal(transportCalls.length,beforeReads,'A save finishing after navigation cannot invalidate the new Space');
  transport.setScope({space:'a',owner:'alpha'});assert.equal((await transport.read('/api/v1/providers')).version,1,'Save invalidation preserves visible content while refreshing');await tick();assert.equal((await transport.read('/api/v1/providers')).version,2);
  await transport.read('/api/v1/agent/routing');await tick();assert.equal(transportCalls.filter(call=>call.path.includes('/agent/routing')).length,2,'Saving providers invalidates associated routing data');
  transport.resources.clear();
}finally{globalThis.fetch=originalFetch;}
console.log('Shared transport save invalidation and captured-scope fencing passed');
