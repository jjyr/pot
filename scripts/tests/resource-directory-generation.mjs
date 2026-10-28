import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import {createMemoryResourceCopies} from './resource-copies-memory.mjs';
registerHooks({resolve(specifier,context,next){return specifier==='/wails/runtime.js'?{url:new URL('./fixtures/native-bridge/runtime.mjs',import.meta.url).href,shortCircuit:true}:next(specifier,context);}});
const deferred=()=>{let resolve;return{promise:new Promise(done=>resolve=done),resolve:value=>resolve(value)};};
const originalFetch=globalThis.fetch;
try{
  globalThis.fakeNative={call:async()=> '[]',on:()=>()=>{}};
  globalThis.fetch=async()=>new Response('[]',{headers:{'Content-Type':'application/json'}});
  for(const desktop of [true,false]){
    for(const boundary of ['enumeration','transaction']){
      const copies=createMemoryResourceCopies(),entered=deferred(),gate=deferred();let source,retireCount=0;
      copies.sources=async()=>{if(boundary==='enumeration'){entered.resolve();await gate.promise;}return[source];};
      const retire=copies.retire.bind(copies);copies.retire=async(...args)=>{if(boundary==='transaction'){entered.resolve();await gate.promise;}await retire(...args);retireCount++;};
      const client=await createProductionClient({desktop,startupIdentity:{runtime_id:'control',boot_id:'boot-1'},resourceStorage:createMemoryProjectionStore(),resourceCopies:copies});
      client.setScope({space:'local',owner:'alice'});client.spaces=[{id:'local',control_id:'control'}];client.assistants=[{id:'alice',space_id:'local'}];client.confirmStartup({runtime_id:'control',boot_id:'boot-1'});
      source={host:JSON.stringify([desktop?'native':'','','control']),control:'control',space:'local',owner:'alice'};
      const old=client.transport('/api/v1/assistants',{space:'local',owner:''});await entered.promise;
      client.suspendStartup();client.confirmStartup({runtime_id:'control',boot_id:'boot-2'});
      gate.resolve();await assert.rejects(old,{name:'AbortError'});
      assert.equal(retireCount,0,'A retired directory must not revoke replacement copies');
      assert.equal((await copies.token(source)).retired,false);
      // An independently current observation must still retire a removed owner.
      await client.transport('/api/v1/assistants',{space:'local',owner:''});
      assert.equal(retireCount,1);assert.equal((await copies.token(source)).retired,true);
      client.resources.clear();copies.close();
      console.log(`PASS ${desktop?'native':'HTTP'} directory retirement during ${boundary} rejects stale read and preserves replacement copies; fresh removal still retires`);
    }
  }
}finally{globalThis.fetch=originalFetch;delete globalThis.fakeNative;}
