import assert from 'node:assert/strict';
import { createWSLifecycle } from '../../web/src/state/ws-lifecycle.mjs';

function eventTarget(initial={}) {
  const listeners=new Map();
  const target=Object.assign({
    addEventListener(type,cb){ if(!listeners.has(type)) listeners.set(type,new Set()); listeners.get(type).add(cb); },
    removeEventListener(type,cb){ const s=listeners.get(type); if(s) s.delete(cb); },
    dispatchEvent(event){ const type=typeof event==='string'?event:event.type; const ev=typeof event==='string'?{type,target}:event; const s=listeners.get(type); if(s) for(const cb of [...s]) cb.call(target,ev); },
    listenerCount(){ let n=0; for(const s of listeners.values()) n+=s.size; return n; }
  }, initial);
  return target;
}
function makeHost({online=true,fetchImpl,noFetch=false}={}) {
  let nowMs=0; let nextId=1; const timers=new Map();
  const host=eventTarget(); const page=eventTarget({hidden:false,visibilityState:'visible'});
  host.navigator={onLine:online}; host.document=page;
  function runDue(){ let ran=true; while(ran){ ran=false; const due=[...timers.values()].filter(t=>t.at<=nowMs).sort((a,b)=>a.at-b.at||a.id-b.id); for(const t of due){ if(!timers.has(t.id)) continue; timers.delete(t.id); t.cb(); ran=true; } } }
  host.setTimeout=(cb,ms)=>{ const id=nextId++; timers.set(id,{id,at:nowMs+Number(ms||0),cb}); return id; };
  host.clearTimeout=id=>{ timers.delete(id); };
  host.now=()=>nowMs;
  host.advance=ms=>{ nowMs+=ms; runDue(); };
  host.jump=ms=>{ nowMs+=ms; };
  host.runTimer=id=>{ const t=timers.get(id); if(t){ timers.delete(id); t.cb(); } };
  host.timers=timers; host.page=page; host.fetchCalls=[];
  if(!noFetch){ const impl=fetchImpl||(()=>Promise.reject(new Error('unexpected fetch'))); host.fetch=(url,init)=>{ host.fetchCalls.push({url,init}); return impl(url,init); }; }
  return host;
}
function res(status,body){ return {status,json:async()=>body}; }
function deferred(){ let resolve,reject; const promise=new Promise((res,rej)=>{ resolve=res; reject=rej; }); return {promise,resolve,reject}; }
async function flush(){ for(let i=0;i<20;i++) await Promise.resolve(); }

const tests=[];
function test(name,fn){ tests.push({name,fn}); }

test('terminal 200-false,401,403 pause', async()=>{
  const cases=[
    [200,{authenticated:false},{retry:false,status:401,code:'unauthorized',uiKey:'connection.authenticationRequired'}],
    [401,undefined,{retry:false,status:401,code:'unauthorized',uiKey:'connection.authenticationRequired'}],
    [403,undefined,{retry:false,status:403,code:'forbidden',uiKey:'connection.streamUnavailable'}]
  ];
  for(const [status,body,expected] of cases){
    const host=makeHost({fetchImpl:()=>Promise.resolve(res(status,body))});
    const denied=[]; const lc=createWSLifecycle({host,onDenied:r=>denied.push(r)});
    const p=lc.failed(); assert.equal(lc.canConnect(),false); const r=await p;
    assert.deepEqual(r,expected); assert.equal(lc.canConnect(),false); assert.equal(denied.length,1); assert.deepEqual(denied[0],expected);
    const again=await lc.failed(); assert.deepEqual(again,expected); assert.equal(host.fetchCalls.length,1); assert.equal(denied.length,1); lc.close();
  }
});

test('authenticated true retries', async()=>{
  const host=makeHost({fetchImpl:()=>Promise.resolve(res(200,{authenticated:true}))});
  const lc=createWSLifecycle({host}); const p=lc.failed(); assert.equal(lc.canConnect(),false);
  const r=await p; assert.deepEqual(r,{retry:true}); assert.equal(lc.canConnect(),true); lc.close();
});

test('network, non-auth HTTP, invalid JSON retry', async()=>{
  const impls=[
    ()=>Promise.reject(new Error('net')),
    ()=>Promise.resolve(res(500,{})),
    ()=>Promise.resolve({status:200,json:async()=>{ throw new Error('bad json'); }})
  ];
  for(const impl of impls){ const host=makeHost({fetchImpl:impl}); const lc=createWSLifecycle({host}); const r=await lc.failed(); assert.deepEqual(r,{retry:true}); lc.close(); }
});

test('timeout bounds ignoring signal', async()=>{
  let signal; const host=makeHost({fetchImpl:(url,init)=>{ signal=init.signal; return new Promise(()=>{}); }});
  const lc=createWSLifecycle({host,probeMs:100}); const p=lc.failed(); assert.equal(signal.aborted,false);
  host.advance(100); const r=await p; assert.deepEqual(r,{retry:true}); assert.equal(signal.aborted,true); lc.close();
});

test('replacement probe stale', async()=>{
  const d1=deferred(); const d2=deferred(); const host=makeHost({fetchImpl:()=>host.fetchCalls.length===1?d1.promise:d2.promise});
  const denied=[]; const lc=createWSLifecycle({host,onDenied:r=>denied.push(r)});
  const p1=lc.failed(); const p2=lc.failed(); const r1=await p1; assert.deepEqual(r1,{retry:false,stale:true});
  d1.resolve(res(401)); await flush(); assert.equal(denied.length,0); assert.equal(lc.canConnect(),false);
  d2.resolve(res(200,{authenticated:false})); const r2=await p2; assert.deepEqual(r2,{retry:false,status:401,code:'unauthorized',uiKey:'connection.authenticationRequired'}); assert.equal(denied.length,1); lc.close();
});

test('close cleanup', async()=>{
  const pending=deferred(); const denied=[]; const resumed=[]; const offline=[];
  const host=makeHost({fetchImpl:()=>pending.promise}); const lc=createWSLifecycle({host,onDenied:r=>denied.push(r),onResume:()=>resumed.push(1),onOffline:()=>offline.push(1)});
  lc.activity(); assert.ok(host.timers.size>0); const p=lc.failed(); assert.equal(lc.canConnect(),false); lc.close(); const r=await p; assert.deepEqual(r,{retry:false,stale:true});
  assert.equal(lc.canConnect(),false); assert.equal(host.timers.size,0); assert.equal(host.listenerCount(),0); assert.equal(host.page.listenerCount(),0);
  pending.resolve(res(200,{authenticated:false})); host.advance(100000); await flush(); assert.equal(denied.length,0); assert.equal(resumed.length,0); assert.equal(offline.length,0);
});

test('offline browser vs desktop', async()=>{
  const host=makeHost({online:true,fetchImpl:()=>new Promise(()=>{})}); const offs=[]; const resumed=[];
  const lc=createWSLifecycle({host,onOffline:r=>offs.push(r),onResume:()=>resumed.push(1)});
  const p=lc.failed(); host.navigator.onLine=false; host.dispatchEvent('offline'); const r=await p; assert.deepEqual(r,{retry:false,stale:true});
  assert.deepEqual(offs,[{code:'stream_offline',uiKey:'streamDisconnected'}]); assert.equal(lc.canConnect(),false);
  host.navigator.onLine=true; host.dispatchEvent('online'); host.advance(0); assert.equal(resumed.length,1); lc.close();
  const dhost=makeHost({online:false,noFetch:true}); let dOffs=0; const dlc=createWSLifecycle({host:dhost,desktop:true,onOffline:()=>dOffs++});
  dhost.dispatchEvent('offline'); assert.equal(dOffs,0); assert.equal(dlc.canConnect(),true); assert.deepEqual(await dlc.failed(),{retry:true}); dlc.close();
  const vhost=makeHost({online:false}); const vlc=createWSLifecycle({host:vhost,desktop:true}); assert.equal(vlc.canConnect(),true); vhost.page.hidden=true; vhost.page.visibilityState='hidden'; vhost.page.dispatchEvent('visibilitychange'); assert.equal(vlc.canConnect(),false); vhost.page.hidden=false; vhost.page.visibilityState='visible'; vhost.page.dispatchEvent('visibilitychange'); assert.equal(vlc.canConnect(),true); vlc.close();
});

test('wallclock foreground stale with clamped timers', async()=>{
  const host=makeHost({fetchImpl:()=>Promise.reject(new Error('no'))}); const offs=[]; const resumed=[];
  const lc=createWSLifecycle({host,idleMs:1000,onOffline:r=>offs.push(r),onResume:()=>resumed.push(1),now:host.now});
  lc.activity(); const idleId=[...host.timers.keys()][0]; host.jump(100); host.runTimer(idleId); assert.equal(offs.length,0); assert.equal(host.timers.size,1);
  host.jump(2000); host.dispatchEvent('focus'); assert.deepEqual(offs,[{code:'stream_idle_timeout',uiKey:'streamDisconnected'}]); assert.equal(resumed.length,0); host.advance(0); assert.equal(resumed.length,1); lc.close();
});

test('event bursts coalesce onResume', async()=>{
  const host=makeHost({}); const resumed=[]; const lc=createWSLifecycle({host,onResume:()=>resumed.push(1)});
  lc.activity(); host.dispatchEvent('focus'); host.dispatchEvent('pageshow'); host.navigator.onLine=true; host.dispatchEvent('online'); host.page.hidden=false; host.page.visibilityState='visible'; host.page.dispatchEvent('visibilitychange'); assert.equal(resumed.length,0); host.advance(0); assert.equal(resumed.length,1); lc.close();
});

test('hidden gating and probe abort', async()=>{
  const host=makeHost({fetchImpl:()=>new Promise(()=>{})}); const resumed=[]; const lc=createWSLifecycle({host,onResume:()=>resumed.push(1)});
  host.page.hidden=true; host.page.visibilityState='hidden'; host.page.dispatchEvent('visibilitychange'); assert.equal(lc.canConnect(),false); lc.activity(); assert.equal(host.timers.size,0);
  host.page.hidden=false; host.page.visibilityState='visible'; host.page.dispatchEvent('visibilitychange'); host.advance(0); assert.equal(resumed.length,1);
  const p=lc.failed(); host.page.hidden=true; host.page.visibilityState='hidden'; host.page.dispatchEvent('visibilitychange'); const r=await p; assert.deepEqual(r,{retry:false,stale:true}); assert.equal(lc.canConnect(),false);
  const before=resumed.length; host.dispatchEvent('focus'); host.advance(0); assert.equal(resumed.length,before);
  host.page.hidden=false; host.page.visibilityState='visible'; host.page.dispatchEvent('visibilitychange'); host.advance(0); assert.equal(resumed.length,before+1); lc.close();
  host.dispatchEvent('focus'); host.page.hidden=false; host.page.visibilityState='visible'; host.page.dispatchEvent('visibilitychange'); host.advance(0); assert.equal(resumed.length,before+1); assert.equal(host.listenerCount(),0); assert.equal(host.page.listenerCount(),0);
});

test('fetch absent retries', async()=>{
  const host=makeHost({noFetch:true}); const lc=createWSLifecycle({host}); const r=await lc.failed(); assert.deepEqual(r,{retry:true}); assert.equal(lc.canConnect(),true); lc.close();
});

test('hidden clears idle timer, visible rearms watchdog', async()=>{
  const host=makeHost({}); const offs=[]; const resumed=[];
  const lc=createWSLifecycle({host,idleMs:1000,now:host.now,onOffline:r=>offs.push(r),onResume:()=>resumed.push(1)});
  lc.activity(); assert.ok(host.timers.size>0);
  host.page.hidden=true; host.page.visibilityState='hidden'; host.page.dispatchEvent('visibilitychange'); assert.equal(host.timers.size,0);
  host.jump(100); host.page.hidden=false; host.page.visibilityState='visible'; host.page.dispatchEvent('visibilitychange'); host.advance(0);
  assert.equal(resumed.length,1);
  host.advance(900); assert.equal(offs.length,1); assert.deepEqual(offs[0],{code:'stream_idle_timeout',uiKey:'streamDisconnected'});
  host.advance(1000); assert.equal(offs.length,1);
  lc.close();
});

test('failed clears stale activity', async()=>{
  const d=deferred(); const host=makeHost({fetchImpl:()=>d.promise}); const offs=[]; const resumed=[];
  const lc=createWSLifecycle({host,idleMs:1000,now:host.now,onOffline:r=>offs.push(r),onResume:()=>resumed.push(1)});
  lc.activity(); host.jump(2000);
  const p=lc.failed();
  host.dispatchEvent('focus'); host.advance(0); assert.equal(offs.length,0);
  d.resolve(res(200,{authenticated:true})); const r=await p; assert.deepEqual(r,{retry:true});
  assert.equal(offs.length,0); lc.close();
});

test('probe timeout bounds hung json', async()=>{
  const host=makeHost({fetchImpl:()=>Promise.resolve({status:200,json:()=>new Promise(()=>{})})});
  let signal; const origFetch=host.fetch;
  host.fetch=(url,init)=>{ signal=init.signal; return origFetch(url,init); };
  const lc=createWSLifecycle({host,probeMs:100});
  const p=lc.failed(); await flush(); assert.equal(signal.aborted,false);
  host.advance(100); const r=await p; assert.deepEqual(r,{retry:true}); assert.equal(signal.aborted,true); lc.close();
});

test('missed online event recovered by focus', async()=>{
  const host=makeHost({online:true,fetchImpl:()=>Promise.resolve(res(200,{authenticated:true}))}); const resumed=[];
  const lc=createWSLifecycle({host,onResume:()=>resumed.push(1)});
  host.navigator.onLine=false;
  host.dispatchEvent('offline');
  assert.equal(lc.canConnect(),false);
  host.navigator.onLine=true; host.dispatchEvent('focus'); host.advance(0);
  assert.equal(lc.canConnect(),true); assert.equal(resumed.length,1);
  lc.close();
});

test('accessConfirmed reauthorization', async () => {
  let fetchCount = 0;
  const denied = [];
  let resumed = 0;
  const host = makeHost({
    fetchImpl: async () => {
      fetchCount++;
      if (fetchCount === 1) return res(401, { error: 'unauthorized' });
      if (fetchCount === 2) return res(200, { authenticated: true });
      return res(403, { error: 'forbidden' });
    },
  });
  const lc = createWSLifecycle({
    host,
    onDenied: (r) => denied.push(r),
    onResume: () => resumed++,
  });
  const failedResult = await lc.failed();
  assert.equal(failedResult.status, 401);
  assert.equal(lc.canConnect(), false);
  assert.equal(fetchCount, 1);
  assert.equal(resumed, 0);
  await lc.accessConfirmed();
  assert.equal(lc.canConnect(), true);
  assert.equal(fetchCount, 1);
  assert.equal(resumed, 0);
  assert.deepEqual(await lc.failed(), { retry: true });
  assert.equal((await lc.failed()).status, 403);
  assert.equal(denied.length, 2);
  lc.close();
  await lc.accessConfirmed();
  assert.equal(lc.canConnect(), false);
});

let failed=false;
for(const t of tests){ try{ await t.fn(); console.log('ok '+t.name); }catch(e){ failed=true; console.error('not ok '+t.name); console.error(e); } }
if(failed) process.exitCode=1;
