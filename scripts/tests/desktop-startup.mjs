import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {setImmediate as turn} from 'node:timers/promises';
import {readFileSync} from 'node:fs';
import {completeStartup,validStartup} from '../../web/src/state/startup-identity.mjs';
import {readDesktopStartup} from '../../web/src/adapters/production.mjs';

registerHooks({load(url,context,next){if(process.env.POT_STARTUP_BASELINE&&url.includes('/web/src/app.js?native-startup='))return {format:'module',source:readFileSync(process.env.POT_STARTUP_BASELINE,'utf8'),shortCircuit:true};return next(url,context);},resolve(specifier,context,next){
  const fixtures={'./components/ui.js':'app-ui.mjs','./state/startup-identity.mjs':'app-store.mjs','./adapters/access.mjs':'app-access.mjs'};
  const fixture=specifier==='/wails/runtime.js'?'runtime.mjs':context.parentURL?.includes('/web/src/app.js')?fixtures[specifier]:null;
  return fixture?{url:new URL('./fixtures/native-bridge/'+fixture,import.meta.url).href,shortCircuit:true}:next(specifier,context);
}});
const locator={schema_version:1,key:'last',runtime_id:'control',boot_id:'boot-1',space:'control',owner:'assistant',surface:'main'};
assert.equal(completeStartup(locator),true);
for(const key of ['runtime_id','boot_id','space','owner','surface'])assert.equal(completeStartup({...locator,[key]:''}),false,`Desktop requires ${key}`);
assert.equal(validStartup({...locator,owner:'',space:'',surface:''}),true,'Existing v1 runtime-only records keep their persisted format');
const originalTimeout=globalThis.setTimeout,originalClear=globalThis.clearTimeout,originalError=console.error;
const traces=[];
let scenarioNumber=0;
class Element {
  children=[];dataset={};isConnected=false;
  setAttribute(){}
  replaceChildren(...children){this.children=children;}
  append(...children){this.children.push(...children);for(const child of children)child.isConnected=true;}
  remove(){this.isConnected=false;}
}
async function scenario(name,saved,run){
  const timers=new Map(),calls=[],mounts=[],saves=[],errors=[],events=[];let timerID=0;
  const state={name,mode:'offline',runtime:{runtime_id:'control',boot_id:'boot-1'},calls,mounts,saves,events};
  globalThis.setTimeout=(fn,ms)=>{const id=++timerID;timers.set(id,{fn,ms});return id;};globalThis.clearTimeout=id=>timers.delete(id);
  console.error=(...args)=>errors.push(args.map(value=>value?.message||String(value)));
  globalThis.window=new EventTarget();
  const root=new Element(),body=new Element();
  globalThis.document={querySelector:()=>root,documentElement:new Element(),body,createElement:()=>new Element(),createTextNode:text=>({text})};
  globalThis.location={search:'?platform=desktop',hash:''};
  Object.defineProperty(globalThis,'navigator',{value:{language:'en'},configurable:true});
  globalThis.fakeNative={on:()=>()=>{},async call(name,...args){
    const method=name.split('.').at(-1);calls.push({method,args});
    if(state.mode==='offline')throw Error('synthetic local Control unavailable');
    if(method==='AssistantReads'){if(state.mode==='reads-failed')throw Error('synthetic reads unavailable');return [];}
    if(method==='Call'){
      if(state.mode==='hang')return new Promise(resolve=>{state.resolveRuntime=resolve;});
      return JSON.stringify(state.runtime);
    }
  }};
  globalThis.startupScenario={store:{async read(){return saved;},async save(runtime,scope={},surface=''){const value={schema_version:1,key:'last',runtime_id:runtime.runtime_id||runtime.id,boot_id:runtime.boot_id||'',space:scope.space||'',owner:scope.owner||'',surface};saves.push(value);saved=value;return value;},async clear(){saved=null;},close(){events.push('store closed');}},mount(_root,client){mounts.push(client);events.push(['mount',client.runtime.runtime_id,client.onlineReady]);client.onStartupConfirmed?.(()=>events.push(['confirmed',client.onlineReady,client.connectionGeneration,client.runtime.boot_id]));return()=>events.push('disposed');}};
  try{
    await import(`../../web/src/app.js?native-startup=${++scenarioNumber}`);
    for(let i=0;i<5;i++)await turn();
    await run(state,{timers,errors,body});
    traces.push({name,calls,saves,events,errors});
  }finally{window.dispatchEvent(new Event('pagehide'));for(const client of mounts)client.resources.clear();}
}
try{
  await scenario('complete cached identity, unavailable local Control',locator,async state=>{
    assert.equal(state.mounts.length,1);const client=state.mounts[0];
    assert.equal(client.onlineReady,false);assert.equal(client.operationsReady,false);assert.equal(client.sessionReady(locator),false);
    assert.deepEqual(state.calls.map(value=>value.method),['Call'],'Retained client must not await AssistantReads before mounting');
    await assert.rejects(client.transport('/api/v1/messages',{method:'POST',body:{request_id:'synthetic'}}),error=>error.code==='startup_unconfirmed');
    await client.rememberStartup({space:'control',owner:'other'},'main');assert.equal(state.saves.length,0,'An offline selection cannot overwrite a confirmed locator');
    state.mode='online';await client.revalidateStartup();
    assert.equal(client.onlineReady,true);assert.equal(client.operationsReady,false,'Runtime confirmation still needs the fresh directory barrier');
    assert.equal(client.sessionReady(locator),false);
    client.directoryGeneration=client.connectionGeneration;client.directorySpaces.add('control');client.assistants=[{id:'assistant',space_id:'control'}];
    await client.rememberStartup({space:'control',owner:'assistant'},'main');assert.equal(completeStartup(state.saves.at(-1)),true);
    window.dispatchEvent(new Event('offline'));assert.equal(client.onlineReady,true,'Internet connectivity is independent of a working local Control');
    state.runtime={runtime_id:'control',boot_id:'boot-2'};await client.revalidateStartup();
    assert.equal(client.runtime.boot_id,'boot-2');assert.equal(client.operationsReady,false);assert.equal(client.sessionReady(locator),false);
    state.runtime={runtime_id:'other-control',boot_id:'boot-3'};await client.revalidateStartup();
    assert.equal(state.mounts.length,2);assert.equal(state.mounts[1].runtime.runtime_id,'other-control');assert.equal(state.mounts[1].operationsReady,false);
    assert.equal(state.events.includes('disposed'),true,'Changed Control disposes the old UI before mounting a new namespace');
  });
  for(const value of [null,{...locator,owner:''},{...locator,boot_id:''}])await scenario('unknown or incomplete identity does not mount',value,async state=>{assert.equal(state.mounts.length,0);assert.equal(state.saves.length,0);});
  await scenario('AssistantReads failure retains cache without authority',locator,async state=>{const client=state.mounts[0];state.mode='reads-failed';await client.revalidateStartup();assert.equal(client.onlineReady,false);assert.equal(state.mounts.length,1);});
  await scenario('bounded native runtime read and late completion',locator,async(state,{timers})=>{
    const client=state.mounts[0];state.mode='hang';const pending=client.revalidateStartup();for(let i=0;i<5;i++)await turn();
    const deadline=[...timers.values()].find(value=>value.ms===10000);assert.ok(deadline);deadline.fn();await pending;
    assert.equal(client.onlineReady,false);state.resolveRuntime(JSON.stringify(state.runtime));await turn();assert.equal(client.onlineReady,false,'A timed-out native result cannot confirm startup');
    state.mode='online';await client.revalidateStartup();assert.equal(client.onlineReady,true);
    state.mode='hang';const old=client.revalidateStartup();for(let i=0;i<5;i++)await turn();const resolveOld=state.resolveRuntime;
    state.mode='online';await client.revalidateStartup();await old;
    resolveOld(JSON.stringify({runtime_id:'late-wrong-control',boot_id:'late-boot'}));await turn();
    assert.equal(state.mounts.length,1);assert.equal(client.runtime.runtime_id,'control','A superseded verification cannot replace the current namespace');
  });
  // The exported identity read obeys cancellation even when a native binding
  // never settles and offers no cancel method.
  const abort=new AbortController();globalThis.fakeNative.call=()=>new Promise(()=>{});
  const hanging=readDesktopStartup({signal:abort.signal});await turn();abort.abort();await assert.rejects(hanging,error=>error.name==='AbortError');
  console.log(JSON.stringify({scenarios:traces},null,2));
}finally{globalThis.setTimeout=originalTimeout;globalThis.clearTimeout=originalClear;console.error=originalError;}
