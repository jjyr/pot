import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {setImmediate as nextTurn} from 'node:timers/promises';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import {installWSFixture} from './helpers/ws-fixture.mjs';

registerHooks({resolve(specifier,context,next){return specifier==='/wails/runtime.js'?{url:new URL('./fixtures/native-bridge/runtime.mjs',import.meta.url).href,shortCircuit:true}:next(specifier,context);}});

const calls=[];
globalThis.fakeNative={
  on(){return()=>{};},
  async call(name,...args){
    const method=name.split('.').at(-1);calls.push({method,args});
    if(method==='Transport')return {url:'ws://localhost/api/v1/ws'};
    if(method==='AssistantReads')return [];
    throw new Error(`unexpected native call ${name}`);
  },
};

const oldFetch=globalThis.fetch;
globalThis.fetch=async()=>new Response(JSON.stringify({runtime_id:'control',boot_id:'boot-1'}),{status:200,headers:{'content-type':'application/json'}});

const helper=installWSFixture();
const frame=(cursor)=>({kind:'reset',base_cursor:0,view:{state:{boot_id:'boot-1',conversation_id:'conversation',cursor},messages:[],replies:[],history:{before:'',after:'',has_more:false,has_newer:false}}});
const activeEvents=()=>{for(const socket of helper.sockets){if(socket.readyState!==1)continue;for(const [id,path] of socket.subscriptions){if(/events/.test(path))return{socket,id};}}return null;};

const main=await createProductionClient({desktop:true,base:'/main',resourceStorage:createMemoryProjectionStore()});
main.setScope({space:'control',owner:'assistant'});
const frames=[],statuses=[];
const stop=main.subscribe(value=>frames.push(value.cursor),value=>statuses.push(value));
await helper.flush();
await nextTurn();
await helper.flush();
const first=activeEvents();
assert.ok(first,'expected an events subscription over the transport socket');
assert.equal(statuses.includes('connected'),false,'no connected before data');
first.socket.emit({type:'event',id:first.id,event:'conversation',event_id:'boot-1:1',data:frame(10)});
assert.deepEqual(frames,['10']);
stop();
first.socket.emit({type:'event',id:first.id,event:'conversation',event_id:'boot-1:2',data:frame(999)});
assert.deepEqual(frames,['10'],'late frame for a stopped subscription is ignored');

const newFrames=[];
const stopNew=main.subscribe(value=>newFrames.push(value.cursor),()=>{},{space:'control',owner:'new-assistant'});
await helper.flush();
await nextTurn();
await helper.flush();
const second=activeEvents();
assert.ok(second,'expected a replacement events subscription');
assert.notEqual(second.id,first.id,'new scope uses a fresh subscription id');
second.socket.emit({type:'event',id:second.id,event:'conversation',event_id:'boot-1:3',data:frame(11)});
first.socket.emit({type:'event',id:first.id,event:'conversation',event_id:'boot-1:4',data:frame(999)});
assert.deepEqual(newFrames,['11'],'retired subscription id rejects late frames');

assert.equal(calls.some(c=>['Call','Subscribe','Unsubscribe'].includes(c.method)),false,'native binding must not own stream lifecycle');

stopNew();main.resources.clear();helper.restore();globalThis.fetch=oldFetch;
console.log('pass');
