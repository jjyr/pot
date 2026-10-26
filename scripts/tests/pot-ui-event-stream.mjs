import assert from 'node:assert/strict';
import {createEventStream} from '../../web/src/state/event-stream.mjs';
function fixture(){
 let time=1,sequence=0;const timers=new Map(),sources=[],statuses=[],received=[];
 class Source extends EventTarget{
  static CLOSED=2;
  constructor(url){super();this.url=url;this.readyState=0;sources.push(this);}
  close(){this.readyState=2;}
  emit(type,data='{}'){if(type==='open')this.readyState=1;const event=new MessageEvent(type,{data});this.dispatchEvent(event);this['on'+type]?.(event);}
 }
 const page=new EventTarget();page.visibilityState='visible';
 const host=Object.assign(new EventTarget(),{EventSource:Source,navigator:{onLine:true},setTimeout(fn,delay){const id=++sequence;timers.set(id,{at:time+delay,fn});return id;},clearTimeout(id){timers.delete(id);}});
 const tick=ms=>{const end=time+ms;for(;;){const first=[...timers].filter(([,v])=>v.at<=end).sort((a,b)=>a[1].at-b[1].at)[0];if(!first)break;time=first[1].at;timers.delete(first[0]);first[1].fn();}time=end;};
 const start=(extra={})=>createEventStream({url:'/owner/a/events',host,page,now:()=>time,random:()=>.5,events:{conversation:(event,initial)=>{const value=JSON.parse(event.data);if(initial&&value.kind!=='reset')throw Error('missing reset');received.push(value);}},onStatus:(...args)=>statuses.push(args),...extra});
 const visible=value=>{page.visibilityState=value?'visible':'hidden';page.dispatchEvent(new Event('visibilitychange'));};
 const current=()=>sources.at(-1),reset=()=>current().emit('conversation','{"kind":"reset"}');
 return {host,page,tick,start,visible,current,reset,sources,statuses,received,timers};
}
{
 const f=fixture(),stop=f.start();f.tick(20000);assert.equal(f.sources.length,2,'Missing headers trigger the first replacement immediately');assert.equal(f.sources[0].readyState,2);
 f.tick(20000);assert.equal(f.sources.length,2,'Continuing failure waits for backoff');f.tick(499);assert.equal(f.sources.length,2);f.tick(1);assert.equal(f.sources.length,3);stop();assert.equal(f.timers.size,0);
}
{
 const f=fixture(),stop=f.start();f.current().emit('open');f.tick(10000);f.current().emit('heartbeat');f.tick(10000);
 assert.equal(f.sources.length,2,'A heartbeat cannot satisfy or postpone the initial reset deadline');
 f.reset();f.tick(30000);f.current().emit('heartbeat');f.tick(44999);assert.equal(f.sources.length,2);f.tick(1);assert.equal(f.sources.length,3,'A quiet established stream is replaced after heartbeat expiry');stop();
}
{
 const f=fixture(),stop=f.start();f.reset();const old=f.current();f.visible(false);assert.equal(f.timers.size,0,'Hidden pages pause watchdog/retry timers');f.tick(120000);
 f.visible(true);f.host.dispatchEvent(new Event('focus'));f.host.dispatchEvent(new Event('pageshow'));f.tick(0);
 assert.equal(f.sources.length,2,'Visible/focus/pageshow coalesce and check elapsed wall time');
 old.emit('conversation','{"kind":"reset","late":true}');old.emit('error');assert.equal(f.received.length,1,'Closed generation cannot deliver late data');assert.equal(f.sources.length,2);stop();
}
{
 const f=fixture(),stop=f.start();f.reset();f.host.navigator.onLine=false;f.host.dispatchEvent(new Event('offline'));assert.equal(f.current().readyState,2);f.tick(90000);assert.equal(f.sources.length,1);
 f.host.navigator.onLine=true;f.host.dispatchEvent(new Event('online'));f.tick(0);assert.equal(f.sources.length,2);stop();
}
{
 const f=fixture(),stop=f.start();f.reset();f.current().emit('error');f.tick(0);f.reset();f.current().emit('error');
 f.tick(499);assert.equal(f.sources.length,2,'A reset followed by another immediate failure must not reset backoff');f.tick(1);assert.equal(f.sources.length,3);
 f.reset();f.tick(15000);f.current().emit('heartbeat');f.current().emit('error');f.tick(0);assert.equal(f.sources.length,4,'Stable traffic resets backoff for the next failure');stop();
}
{
 const f=fixture();let signal,resolve;const stop=f.start({terminal:value=>{signal=value;return new Promise(yes=>resolve=yes);}});f.current().readyState=2;f.current().emit('error');await Promise.resolve();assert(signal);assert.equal(f.sources.length,1);
 f.tick(100000);assert.equal(f.sources.length,1,'Terminal responses never retry in a loop');const count=f.statuses.length;stop();assert(signal.aborted,'Unsubscribe cancels the real terminal status request');resolve({uiKey:'connection.authenticationRequired'});await Promise.resolve();await Promise.resolve();assert.equal(f.statuses.length,count,'A terminal result cannot escape its owner');
}
{
 const f=fixture();f.visible(false);const stop=f.start();assert.equal(f.sources.length,0);f.tick(50000);f.visible(true);f.tick(0);assert.equal(f.sources.length,1);f.current().emit('conversation','{"kind":"delta"}');f.tick(0);assert.equal(f.sources.length,2);stop();
}
{
 const f=fixture(),a=f.start();f.reset();a();const b=f.start({url:'/owner/b/events'});f.tick(20000);assert(f.sources.slice(0,-1).every(s=>s.readyState===2));assert.equal(f.current().url,'/owner/b/events');assert(!f.statuses.some(([status])=>status==='connected'),'Only the committed domain may report business readiness');b();
}
// A lifecycle cancellation detaches probe ownership synchronously; settling
// the cancelled promise cannot be required to make online recovery runnable.
{
 const f=fixture();let terminalSignal;
 const stop=f.start({terminal:signal=>{terminalSignal=signal;return new Promise(resolve=>signal.addEventListener('abort',()=>resolve({retry:true}),{once:true}));}});
 f.current().readyState=2;f.current().emit('error');await Promise.resolve();
 f.host.navigator.onLine=false;f.host.dispatchEvent(new Event('offline'));
 f.host.navigator.onLine=true;f.host.dispatchEvent(new Event('online'));
 for(let i=0;i<8;i++)await Promise.resolve();f.tick(0);
 const state={sources:f.sources.length,activeSources:f.sources.filter(source=>source.readyState!==2).length,scheduledTimers:f.timers.size,terminalAborted:terminalSignal.aborted};
 assert.equal(state.activeSources,1,'Offline/online during a cancelled terminal probe must recover: '+JSON.stringify(state));
 assert.equal(state.sources,2);assert.equal(state.terminalAborted,true);
 for(let round=0;round<3;round++){
  f.current().readyState=2;f.current().emit('error');await Promise.resolve();
  f.host.navigator.onLine=false;f.host.dispatchEvent(new Event(round%2?'focus':'offline'));f.host.navigator.onLine=true;f.host.dispatchEvent(new Event('online'));
  for(let i=0;i<8;i++)await Promise.resolve();f.tick(0);assert.equal(f.sources.length,round+3);assert.equal(f.sources.filter(source=>source.readyState!==2).length,1);assert(terminalSignal.aborted);
 }
 stop();assert.equal(f.timers.size,0);
}
{
 const f=fixture(),probes=[],flush=async()=>{for(let i=0;i<8;i++)await Promise.resolve();};
 const stop=f.start({terminal:signal=>new Promise(resolve=>probes.push({signal,resolve}))});
 f.current().readyState=2;f.current().emit('error');await flush();
 f.host.navigator.onLine=false;f.host.dispatchEvent(new Event('offline'));
 f.host.navigator.onLine=true;f.host.dispatchEvent(new Event('online'));f.tick(0);
 f.current().readyState=2;f.current().emit('error');await flush();
 assert.equal(probes.length,2);assert(probes[0].signal.aborted);assert(!probes[1].signal.aborted);
 probes[0].resolve({uiKey:'connection.authenticationRequired'});await flush();
 f.host.dispatchEvent(new Event('focus'));f.host.dispatchEvent(new Event('online'));f.tick(0);
 assert.equal(f.sources.length,2,'The old finally cannot release the newer pending probe');
 assert(!f.statuses.some(([,reason])=>reason?.uiKey==='connection.authenticationRequired'),'A revoked probe cannot publish late authentication failure');
 probes[1].resolve({retry:true});await flush();f.tick(0);assert.equal(f.sources.length,3);assert.equal(f.sources.filter(s=>s.readyState!==2).length,1);stop();
}
{
 const f=fixture();let resolve;const old=f.start({terminal:()=>new Promise(yes=>resolve=yes)});
 f.current().readyState=2;f.current().emit('error');await Promise.resolve();old();
 const next=f.start({url:'/owner/b/events'}),count=f.statuses.length;
 resolve({uiKey:'connection.authenticationRequired'});for(let i=0;i<8;i++)await Promise.resolve();
 f.reset();f.host.dispatchEvent(new Event('focus'));f.tick(0);assert.equal(f.statuses.length,count);assert.equal(f.current().url,'/owner/b/events');assert.equal(f.current().readyState,0);next();
}
{
 const f=fixture(),stop=f.start({terminal:async()=>({uiKey:'connection.authenticationRequired',code:'stream_authentication'})});
 f.current().readyState=2;f.current().emit('error');for(let i=0;i<8;i++)await Promise.resolve();
 assert.equal(f.statuses.at(-1)[1].code,'stream_authentication');
 for(let i=0;i<3;i++){
  f.host.dispatchEvent(new Event('focus'));f.host.dispatchEvent(new Event('pageshow'));f.visible(false);f.visible(true);
  f.host.navigator.onLine=false;f.host.dispatchEvent(new Event('offline'));f.host.navigator.onLine=true;f.host.dispatchEvent(new Event('online'));f.tick(100000);
 }
 assert.equal(f.sources.length,1,'Confirmed authentication failures remain paused across timers and lifecycle events');assert.equal(f.timers.size,0);assert.equal(f.statuses.at(-1)[1].code,'stream_authentication');stop();
}
{
 const f=fixture(),failure=Error('status probe network failure'),stop=f.start({terminal:async()=>{throw failure;}});
 f.current().readyState=2;f.current().emit('error');for(let i=0;i<8;i++)await Promise.resolve();f.tick(0);assert.equal(f.sources.length,2);
 assert.equal(f.statuses.at(-2)[1].cause,failure);
 f.current().readyState=2;f.current().emit('error');for(let i=0;i<8;i++)await Promise.resolve();f.tick(499);assert.equal(f.sources.length,2);f.tick(1);assert.equal(f.sources.length,3,'Unknown probe failures retain bounded backoff');stop();
}
{
 const f=fixture();let calls=0;const stop=f.start({terminal:()=>{calls++;return {retry:true};}});
 f.current().readyState=2;f.current().emit('error');stop();
 for(let i=0;i<8;i++)await Promise.resolve();f.tick(100000);f.host.dispatchEvent(new Event('online'));f.host.dispatchEvent(new Event('focus'));f.tick(0);
 assert.equal(calls,0,'Disposal before the probe microtask prevents subsequent IO');assert.equal(f.sources.length,1);assert.equal(f.timers.size,0);
}
console.log('Event streams: bounded deadlines/backoff, lifecycle recovery, real probe abort, probe identity/generation fences, owner isolation and confirmed-auth pause passed');
