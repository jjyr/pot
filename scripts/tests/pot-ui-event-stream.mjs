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
console.log('Event streams: bounded first reset, heartbeat liveness, backoff, paused clocks, lifecycle coalescing, terminal cancellation and owner fencing passed');
