import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import {createMemoryResourceCopies} from './resource-copies-memory.mjs';
import {createQueryRegistry,createCommands} from '../../web/src/state/domains/query.mjs';
import {createDirectoryDomain} from '../../web/src/state/domains/directory.mjs';
registerHooks({resolve(specifier,context,next){return specifier==='/wails/runtime.js'?{url:new URL('./fixtures/native-bridge/runtime.mjs',import.meta.url).href,shortCircuit:true}:next(specifier,context);}});
const deferred=()=>{let resolve;return{promise:new Promise(done=>resolve=done),resolve:value=>resolve(value)};};
const originalFetch=globalThis.fetch;
try{
  globalThis.fakeNative={call:async()=> '[]',on:()=>()=>{}};
  globalThis.fetch=async()=>new Response('[]',{headers:{'Content-Type':'application/json'}});
  for(const desktop of [true,false]){
    for(const retirement of ['startup','directory-authority']){
    for(const boundary of ['enumeration','transaction']){
      const copies=createMemoryResourceCopies(),entered=deferred(),gate=deferred();let source,retireCount=0;
      copies.sources=async()=>{if(boundary==='enumeration'){entered.resolve();await gate.promise;}return[source];};
      const retire=copies.retire.bind(copies);copies.retire=async(...args)=>{if(boundary==='transaction'){entered.resolve();await gate.promise;}await retire(...args);retireCount++;};
      const client=await createProductionClient({desktop,startupIdentity:{runtime_id:'control',boot_id:'boot-1'},resourceStorage:createMemoryProjectionStore(),resourceCopies:copies});
      client.setScope({space:'local',owner:'alice'});client.spaces=[{id:'local',control_id:'control'}];client.assistants=[{id:'alice',space_id:'local'}];client.confirmStartup({runtime_id:'control',boot_id:'boot-1'});
      source={host:JSON.stringify([desktop?'native':'','','control']),control:'control',space:'local',owner:'alice'};
      const old=client.transport('/api/v1/assistants',{space:'local',owner:''});await entered.promise;
      if(retirement==='startup'){client.suspendStartup();client.confirmStartup({runtime_id:'control',boot_id:'boot-2'});}
      else{
        client.directoryGeneration=-1;client.directorySpaces.clear();client.directoryAuthorityRevision++;
        client.directoryGeneration=client.connectionGeneration;client.directorySpaces=new Set(['local']);client.directoryAuthorityRevision++;
      }
      gate.resolve();await assert.rejects(old,{name:'AbortError'});
      assert.equal(retireCount,0,'A retired directory must not revoke replacement copies');
      assert.equal((await copies.token(source)).retired,false);
      // An independently current observation must still retire a removed owner.
      await client.transport('/api/v1/assistants',{space:'local',owner:''});
      assert.equal(retireCount,1);assert.equal((await copies.token(source)).retired,true);
      client.resources.clear();copies.close();
      console.log(`PASS ${desktop?'native':'HTTP'} ${retirement} retirement during ${boundary} rejects stale directory read and preserves replacement copies; fresh removal still retires`);
    }
    }
  }
  // Exercise the actual shared directory failure/recovery path, including its
  // revision setter, through production clients and memory persistence faults.
for(const desktop of [true,false]){
  const storage=createMemoryProjectionStore(),copies=createMemoryResourceCopies(),entered=deferred(),gate=deferred();let fault=false,pause=false,retireCount=0;
  const commit=storage.commit.bind(storage);storage.commit=async(...args)=>{if(fault)throw Object.assign(Error('synthetic directory commit failure'),{storage:true});return commit(...args);};
  const scopes={host:JSON.stringify([desktop?'native':'','','control']),control:'control',space:'local',owner:'alice'};
  copies.sources=async()=>{if(pause){pause=false;entered.resolve();await gate.promise;}return[scopes];};
  const retire=copies.retire.bind(copies);copies.retire=async(...args)=>{await retire(...args);retireCount++;};
  let oldRequest=false;
  const response=path=>{path=path.split('?')[0];if(path==='/api/v1/spaces')return{items:[{id:'local',control_id:'control',connected:true}],active_space_id:'local'};if(path==='/api/v1/assistants')return oldRequest?[]:[{id:'alice',capabilities:{version:1,operations:['chat.read','chat.send']}}];if(path==='/api/v1/runtime')return{runtime_id:'control',boot_id:'boot'};if(path==='/api/v1/agent-links')return{items:[]};throw Error(path);};
  globalThis.fakeNative={call:async(name,method,path)=>JSON.stringify(response(path)),on:()=>()=>{}};
  globalThis.fetch=async path=>new Response(JSON.stringify(response(path)),{headers:{'Content-Type':'application/json'}});
  const client=await createProductionClient({desktop,startupIdentity:{runtime_id:'control',boot_id:'boot'},resourceStorage:storage,resourceCopies:copies});client.confirmStartup({runtime_id:'control',boot_id:'boot'});client.setScope({space:'local',owner:'alice'});
  const queries=createQueryRegistry(client.resources),commands=createCommands(client),directory=createDirectoryDomain(client,{resources:client.resources,queries,commands});
  await directory.refresh();assert(client.operationsReady);const revision=client.directoryAuthorityRevision;
  oldRequest=pause=true;const pending=client.transport('/api/v1/assistants',{space:'local',owner:''});await entered.promise;oldRequest=false;
  fault=true;await assert.rejects(directory.refresh(),error=>error.storage);assert.equal(client.operationsReady,false);
  fault=false;await directory.refresh();assert(client.operationsReady);assert(client.directoryAuthorityRevision>revision);
  gate.resolve();let staleRejected=false;try{await pending;}catch(error){staleRejected=error.name==='AbortError';}
  console.log(JSON.stringify({desktop,staleRejected,retireCount,operationsReady:client.operationsReady,revision:client.directoryAuthorityRevision,generation:client.connectionGeneration}));
  queries.dispose();commands.dispose();client.resources.clear();copies.close();assert.equal(retireCount,0);assert(staleRejected);
}
}finally{globalThis.fetch=originalFetch;delete globalThis.fakeNative;}
