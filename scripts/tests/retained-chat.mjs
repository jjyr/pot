import assert from 'node:assert/strict';
import {retainCommittedChat} from '../../web/src/state/retained-chat.mjs';
import {createChatDomain} from '../../web/src/state/domains/chat.mjs';

const row = n => ({id:'m'+n,sequence:String(n),version:1,role:'user',text:'row '+n,status:'complete'});

function frame(messages, total, overrides={}){
  return {
    kind:'reset', base_cursor:'0',
    runtime_id:'r',boot_id:'b',owner_id:'owner',conversation_id:'c',cursor:'1',running:false,turn_id:null,replies:[],
    messages,
    history:{before:messages.length?String(messages[0].sequence):'',after:messages.length?String(messages.at(-1).sequence):'',has_more:false,has_newer:false},
    _latest:String(total),
    ...overrides,
  };
}

const normals = view => view.messages.filter(m=>!m.context_only);
const sequences = view => normals(view).map(m=>BigInt(m.sequence));

function contiguous(seqs){
  for(let i=1;i<seqs.length;i++) if(seqs[i]!==seqs[i-1]+1n) return false;
  return true;
}

function assertNoGap(view,label){
  const seqs=sequences(view);
  assert.ok(seqs.length>0, label+': no normals');
  assert.ok(contiguous(seqs), label+': non-contiguous');
}

// helper: >640 retained normals via end pages
{
  let view=retainCommittedChat(null,frame([],1200),{replace:true});
  const pushed=[]; for(let i=601;i<601+700;i++) pushed.push(row(i));
  view=retainCommittedChat(view,frame(pushed,1200),{replace:true});
  assert.equal(normals(view).length,700);
  assertNoGap(view,'helper initial');
  let last=BigInt(sequences(view).at(-1));
  for(let round=0;round<3;round++){
    const next=[];
    for(let i=0;i<5;i++){ last+=1n; next.push(row(Number(last))); }
    const incoming=frame(next,1200,{history:{before:String(next[0].sequence),after:String(next.at(-1).sequence),has_more:true,has_newer:false}});
    view=retainCommittedChat(view,incoming);
    assertNoGap(view,'helper round '+round);
  }
  assert.ok(normals(view).length>640);
}

// helper: disjoint reset tail deferred
{
  const base=retainCommittedChat(null,frame(Array.from({length:10},(_,i)=>row(i+1)),100),{replace:true});
  const tail=frame(Array.from({length:5},(_,i)=>row(i+51)),100,{kind:'reset'});
  const merged=retainCommittedChat(base,tail);
  assert.deepEqual(sequences(merged).map(String),Array.from({length:10},(_,i)=>String(i+1)));
  assert.equal(merged.history.has_newer,true);
}

// helper: overlapping updated row preserves old sequence
{
  const base=retainCommittedChat(null,frame([row(1),row(2),row(3)],10),{replace:true});
  const updated={...row(2),version:5,sequence:'2',text:'changed'};
  const merged=retainCommittedChat(base,frame([row(3),updated,row(4)],10));
  const m2=merged.messages.find(m=>m.id==='m2');
  assert.equal(m2.sequence,'2');
  assert.equal(m2.version,5);
  assert.equal(m2.text,'changed');
}

// helper: context-only promotion, direction before
{
  const base=retainCommittedChat(null,frame([row(3),{...row(2),version:7,context_only:true},row(4)],10),{replace:true});
  const incoming=frame([row(2),row(3),row(4)],10);
  const merged=retainCommittedChat(base,incoming,{direction:'before'});
  const m2=merged.messages.find(m=>m.id==='m2');
  assert.equal(m2.context_only,false);
  assert.equal(m2.version,7);
  assert.equal(m2.sequence,'2');
}

// helper: new boot lower version overlap -> 0
{
  const base=retainCommittedChat(null,frame([row(1),row(2),row(3)],10),{replace:true});
  const incoming=frame([{...row(2),version:0,text:'low'},row(3),row(4)],10,{boot_id:'b2'});
  const merged=retainCommittedChat(base,incoming);
  assert.equal(merged.boot_id,'b2');
  const m2=merged.messages.find(m=>m.id==='m2');
  assert.equal(m2.sequence,'2');
  assert.equal(m2.version,0);
}

// helper: different conversation replaces
{
  const base=retainCommittedChat(null,frame([row(1),row(2)],10),{replace:true});
  const merged=retainCommittedChat(base,frame([row(5),row(6)],10,{conversation_id:'other'}));
  assert.deepEqual(sequences(merged).map(String),['5','6']);
}

function create({total=1200,initial=[401,480],pageSize=80,expose=true,historyOverride=null}={}){
  const subs=[]; const calls=[];
  function makePage(lo,hi){
    if(lo>hi) return frame([],total);
    const msgs=[]; for(let n=lo;n<=hi;n++) msgs.push(row(n));
    return frame(msgs,total,{history:{before:String(lo),after:String(hi),has_more:lo>1,has_newer:hi<total}});
  }
  function pageFor(q){
    if(q.around){
      const m=/^m(\d+)$/.exec(String(q.around));
      let c=m?Number(m[1]):Math.floor((total+1)/2);
      const half=Math.floor(pageSize/2);
      let lo=c-half,hi=c+half-1;
      if(lo<1)lo=1; if(hi>total)hi=total;
      if(hi-lo+1>pageSize)hi=lo+pageSize-1;
      if(hi-lo+1<pageSize&&lo>1)lo=Math.max(1,hi-pageSize+1);
      return makePage(lo,hi);
    }
    if(q.after!=null){ const lo=Number(q.after)+1; return makePage(lo,Math.min(total,lo+pageSize-1)); }
    if(q.before!=null){ const hi=Number(q.before)-1; return makePage(Math.max(1,hi-pageSize+1),hi); }
    return makePage(Math.max(1,total-pageSize+1),total);
  }
  const client={
    production:false, runtime:{runtime_id:'r'}, spaces:[], uiLocale:()=>'en', connectionGeneration:0,
    async history(q){ calls.push(q); if(historyOverride) return historyOverride(q,pageFor); return pageFor(q); },
    subscribe(cb){subs.push(cb);return ()=>{const i=subs.indexOf(cb);if(i>=0)subs.splice(i,1);};},
    snapshot:async()=>pageFor({}),
    confirmSession(){},
  };
  const initialFrame=()=>{const lo=initial[0],hi=initial[1];const msgs=[];for(let n=lo;n<=hi;n++)msgs.push(row(n));return frame(msgs,total,{history:{before:String(lo),after:String(hi),has_more:lo>1,has_newer:hi<total}});};
  return {
    client, calls,
    async start(domain){
      const session=domain.open({space:'s',owner:'owner'},{surface:'main'});
      const observed=[]; let failedReason=null;
      session.watch({snapshot(v){observed.push(v);},failed(r){failedReason=r;},connection(){},removed(){}});
      const emit=async v=>{for(const cb of subs.slice()) await cb(v);};
      await emit(initialFrame());
      await Promise.resolve();
      if(expose) session.expose();
      return {session,observed,emit,getFailed:()=>failedReason};
    },
  };
}

// domain: latest and earlier bridge contiguous
{
  const fx=create();
  const domain=createChatDomain(fx.client);
  const {session}=await fx.start(domain);
  const around=await session.history({around:'m1000'},{around:true});
  assert.ok(sequences(around).some(s=>s===1000n));
  assertNoGap(around,'around');
  assert.equal(normals(around).length,640);
  assert.equal(sequences(around).at(-1),1040n);
  const latest=await session.history({},{around:true});
  assertNoGap(latest,'latest');
  assert.equal(sequences(latest).at(-1),1200n);
  assert.equal(normals(latest).length,800);
  const before=fx.calls.length;
  const older=await session.history({around:'m50'},{around:true});
  const oldSeqs=sequences(older);
  assert.ok(oldSeqs.some(s=>s===50n));
  assertNoGap(older,'older');
  assert.ok(oldSeqs.at(-1)>=1200n);
  assert.ok(normals(older).length>640);
  assert.ok(fx.calls.length-before>1);
  await session.close(); domain.dispose();
}

// domain: cancellation via currentPosition
{
  const fx=create();
  const domain=createChatDomain(fx.client);
  const {session}=await fx.start(domain);
  let active=true;
  let releases=[];
  const origHistory=fx.client.history;
  fx.client.history=async q=>{
    await new Promise(r=>releases.push(r));
    return origHistory(q);
  };
  const p=session.history({around:'m1000'},{around:true,currentPosition:()=>active});
  await Promise.resolve(); await Promise.resolve();
  active=false;
  for(const r of releases) r();
  const result=await p;
  assert.equal(result,null);
  await session.close(); domain.dispose();
}

// domain: valid invalidation throws AbortError
{
  const fx=create();
  const domain=createChatDomain(fx.client);
  const {session}=await fx.start(domain);
  let valid=true; let releases=[];
  const origHistory=fx.client.history;
  fx.client.history=async q=>{ await new Promise(r=>releases.push(r)); return origHistory(q); };
  const p=session.history({around:'m1000'},{around:true,valid:()=>valid});
  await Promise.resolve(); await Promise.resolve();
  valid=false;
  for(const r of releases) r();
  let threw=null;
  try{ await p; }catch(e){ threw=e; }
  assert.ok(threw);
  assert.equal(threw.name,'AbortError');
  await session.close(); domain.dispose();
}

// domain: no-progress -> history_gap
{
  const fx=create({historyOverride:(q,pageFor)=>{
    if(q.after!=null){
      const lo=481,hi=560,msgs=[];for(let n=lo;n<=hi;n++)msgs.push(row(n));
      return frame(msgs,1200,{history:{before:String(lo),after:String(hi),has_more:true,has_newer:true}});
    }
    if(q.around||q.before!=null){
      const m=/^m(\d+)$/.exec(String(q.around||''));
      const c=m?Number(m[1]):520;
      const lo=Math.max(1,c-40),hi=Math.min(1200,c+39),msgs=[];
      for(let n=lo;n<=hi;n++)msgs.push(row(n));
      return frame(msgs,1200,{history:{before:String(lo),after:String(hi),has_more:lo>1,has_newer:true}});
    }
    return pageFor(q);
  }});
  const domain=createChatDomain(fx.client);
  const {session}=await fx.start(domain);
  let threw=null;
  try{ await session.history({around:'m1000'},{around:true}); }catch(e){ threw=e; }
  assert.ok(threw);
  assert.equal(threw.message,'history_gap');
  await session.close(); domain.dispose();
}

// domain: unexposed around replaces
{
  const fx=create({expose:false});
  const domain=createChatDomain(fx.client);
  const {session}=await fx.start(domain);
  const result=await session.history({around:'m700'},{around:true});
  const seqs=sequences(result);
  assert.ok(seqs.some(s=>s===700n));
  assert.ok(!seqs.some(s=>s===401n));
  assertNoGap(result,'unexposed');
  await session.close(); domain.dispose();
}

{
  let session;
  let domain;
  try {
    let emitFn;
    let announced = false;
    const fx = create({
      historyOverride: async (q, pageFor) => {
        if (q.after === '1120' && !announced) {
          announced = true;
          await emitFn({
            ...frame([row(1201), row(1202)], 1202),
            kind: 'delta',
            base_cursor: '1',
            cursor: '2'
          });
        }
        if (announced && !q.around && !q.before && !q.after) {
          return frame(Array.from({ length: 80 }, (_, i) => row(1123 + i)), 1202);
        }
        if (announced && q.after === '1200') {
          return frame([row(1201), row(1202)], 1202);
        }
        return pageFor(q);
      }
    });
    domain = createChatDomain(fx.client);
    const started = await fx.start(domain);
    session = started.session;
    emitFn = started.emit;

    const first = await session.history({}, { around: true });
    assert.equal(first.history.after, '1200');
    assert.equal(first.history.has_newer, true);
    assertNoGap(first, 'finite latest');

    const second = await session.history({}, { around: true });
    assert.equal(second.history.after, '1202');
    assert.equal(second.history.has_newer, false);
    assertNoGap(second, 'finite latest after delta');

    assert.equal(fx.calls.filter(q => q.after === '1200').length, 1);
  } finally {
    if (session) await session.close();
    if (domain) domain.dispose();
  }
}

{
  const row = n => ({id:'m'+n,sequence:String(n),version:1,role:'user',text:'row '+n,status:'complete'});
  const frame = (messages,total,overrides={}) => ({kind:'reset',runtime_id:'r',owner_id:'owner',conversation_id:'c',boot_id:'b',
    messages,replies:[],history:{before:messages[0]?.sequence||'',after:messages.at(-1)?.sequence||''},_latest:String(total),...overrides});
  const retain = retainCommittedChat;
  let prev = retain(null, frame([1,2,3].map(n=>({...row(n),version:100})), 3));
  let incoming = frame([{...row(3),version:1}], 3, {boot_id:'b2'});
  let snap = retain(prev, incoming);
  assert.equal(snap.messages.find(m=>m.id==='m1').version, 100);
  incoming = frame([{...row(1),text:'fresh'}], 1, {boot_id:'b2'});
  prev = snap;
  snap = retain(prev, incoming, {direction:'before'});
  let m1 = snap.messages.find(m=>m.id==='m1');
  assert.equal(m1.version, 1); assert.equal(m1.text, 'fresh');
  assert.equal(snap.messages.find(m=>m.id==='m2').version, 100);
  incoming = frame([{...row(1),version:0,text:'stale'}], 1, {boot_id:'b2'});
  prev = snap;
  snap = retain(prev, incoming);
  m1 = snap.messages.find(m=>m.id==='m1');
  assert.equal(m1.version, 1); assert.equal(m1.text, 'fresh');
  incoming = frame([row(9)], 9, {boot_id:'b3', conversation_id:'d'});
  prev = snap;
  snap = retain(prev, incoming);
  assert.equal(snap.messages.length, 1);
  assert.equal(snap.messages[0].id, 'm9');
  assert.deepEqual([...snap._retainedBoots.entries()], [['m9','b3']]);
}

{
  const seed = {
    runtime_id: 'r',
    owner_id: 'owner',
    conversation_id: 'c',
    boot_id: 'b',
    kind: 'reset',
    cursor: '1',
    running: false,
    turn_id: null,
    replies: [],
    messages: [
      {
        id: 'plain',
        role: 'assistant',
        text: 'first',
        status: 'complete',
        version: 1
      }
    ]
  };

  const first = retainCommittedChat(null, seed);
  assert.ok(first);

  const second = retainCommittedChat(first, {
    ...seed,
    cursor: '2',
    messages: [
      {
        ...seed.messages[0],
        text: 'second',
        version: 2
      }
    ]
  });

  assert.ok(second);
assert.equal(second.messages[0].text,'second');
assert.equal(second.history.before,'');
assert.equal(second.history.after,'');
assert.equal(second.history.has_more,false);
assert.equal(second.history.has_newer,false);
assert.equal(second.messages[0].sequence,undefined);
}

console.log('retained-chat regression: ok');
