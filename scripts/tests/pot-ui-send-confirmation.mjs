import assert from 'node:assert/strict';
import {createConversationState,beginSubscription,acceptSnapshot,beginSend,settleSend,setDraft,draft,setDraftReferences,draftReferences,ownerKey} from '../../web/src/state/conversation.mjs';
import {createSendConfirmation} from '../../web/src/state/send-confirmation.mjs';
import {createProductionClient} from '../../web/src/adapters/production.mjs';

const flush=async()=>{for(let i=0;i<20;i++)await Promise.resolve();};
class Clock {
  now=0;id=0;timers=new Map();
  setTimeout(fn,delay){const id=++this.id;this.timers.set(id,{fn,at:this.now+delay});return id;}
  clearTimeout(id){this.timers.delete(id);}
  async advance(ms){const end=this.now+ms;while(true){const next=[...this.timers].sort((a,b)=>a[1].at-b[1].at)[0];if(!next||next[1].at>end)break;this.now=next[1].at;this.timers.delete(next[0]);next[1].fn();await flush();}this.now=end;await flush();}
}
const snapshot=(overrides={})=>({runtime_id:'runtime',boot_id:'boot',owner_id:'["space","a"]',conversation_id:'main',cursor:'1',replies:[],running:true,turn_id:'original',messages:[],...overrides});
const user=(request,extra={})=>({id:request.request_id,role:'user',text:request.text,status:'accepted',accepted_request_id:request.request_id,steering:true,accepted_turn_id:request.user_message_id,...extra});
function fixture(){
  const state=createConversationState(),clock=new Clock(),calls=[],queries=[],accepted=[],rejected=[];
  beginSubscription(state);acceptSnapshot(state,snapshot(),state.epoch);
  let queryState='unconfirmed',scope={space:'space',owner:'a'},queryError=false,sendError,online=true;
  const client={production:true,runtime:{runtime_id:'runtime'},spaces:[{id:'space',control_id:'control'}],scope:()=>scope,
    send:(request,options)=>new Promise((resolve,reject)=>{calls.push({request,options,resolve,reject});if(sendError)reject(sendError);}),
    sendReceipt:async(request,options)=>{queries.push({request,options});if(queryError)throw Error('Offline');return {request_id:request.request_id,conversation_id:request.conversation_id,user_message_id:request.user_message_id,retry_user_message_id:request.retry_user_message_id,runtime_id:'control',owner_id:'a',state:queryState,...(queryState==='accepted'?{message_id:'accepted'}:{})};},
  };
  const manager=createSendConfirmation({state,client,clock,now:()=>clock.now,online:()=>online,timeout:10,delays:[1,2,4],changed:()=>{},accepted:r=>accepted.push(r),rejected:r=>rejected.push(r)});
  const start=(text='same text',attachments=[])=>{setDraft(state,text);const r=beginSend(state,'id-'+calls.length,{allowRunning:true,attachments});manager.start(r);return r;};
  return {state,clock,calls,queries,accepted,rejected,manager,client,start,set queryState(v){queryState=v;},set queryError(v){queryError=v;},set scope(v){scope=v;},set sendError(v){sendError=v;},set online(v){online=v;}};
}
// A known submit result owns the normal path. Recovery signals and the old
// first-query deadline must not cause receipt reads while its POST is pending.
for(const delay of [0,9]){
  const f=fixture();f.queryState='accepted';f.start('normal submit',[{id:'submitted'}]);
  await f.clock.advance(delay);await f.manager.resume();await f.manager.resume();
  assert.equal(f.queries.length,0,'A pending POST does not need a parallel acceptance query');
  assert.equal(f.state.pending.size,1,'Only the response or matching SSE can confirm a normal submit');
  f.calls[0].resolve({message_id:'accepted',duplicate:false});await flush();
  assert.equal(f.state.pending.size,0);assert.equal(draft(f.state),'');assert.equal(f.queries.length,0);assert.equal(f.accepted.length,1);assert.equal(f.clock.timers.size,0);f.manager.close();
}
{
  const f=fixture();f.start();await f.clock.advance(3);f.manager.suspend();
  assert(!f.calls[0].options.signal.aborted,'Backgrounding preserves the normal POST wait');
  await f.clock.advance(5);await f.manager.resume();await f.manager.recover([...f.state.pending.values()][0]);
  assert.equal(f.queries.length,0,'Neither foreground nor explicit check may query a submit with no unknown result');
  f.calls[0].resolve({message_id:'accepted',duplicate:false});await flush();assert.equal(draft(f.state),'');assert.equal(f.queries.length,0);f.manager.close();
}
{
  const f=fixture();f.start();await f.clock.advance(9);f.calls[0].reject({status:409});await flush();await f.clock.advance(40);await f.manager.resume();
  assert.equal(f.rejected.length,1);assert.equal(f.queries.length,0,'A definite rejection is not an unknown submission');assert.equal(draft(f.state),'same text');f.manager.close();
}
{
  const f=fixture();f.sendError={accepted:true,uiKey:'storage.commandAccepted'};f.start();await flush();await f.clock.advance(40);
  assert.equal(f.accepted.length,1);assert.equal(draft(f.state),'');assert.equal(f.queries.length,0,'Known acceptance reported by the adapter needs no readback');f.manager.close();
}
{
  const f=fixture();f.start();await f.clock.advance(3);assert.equal(f.queries.length,0);
  f.calls[0].reject(Error('Connection interrupted'));await flush();await f.clock.advance(1);
  assert.equal(f.queries.length,1,'Network failure enables receipt reads only after the POST outcome becomes unknown');assert.equal(f.calls.length,1);f.manager.close();
}
{
  const f=fixture();f.start();await f.clock.advance(3);f.online=false;await f.manager.resume();assert(f.calls[0].options.signal.aborted);
  f.online=true;f.queryState='accepted';await f.manager.resume();
  f.calls[0].reject({status:400});await flush();assert.equal(f.queries.length,1);assert.equal(f.calls.length,1);assert.equal(f.accepted.length,1);assert.equal(f.rejected.length,0,'Late submit failure cannot undo confirmed acceptance');f.manager.close();
}
{
  const f=fixture(),r=f.start();
  assert(acceptSnapshot(f.state,snapshot({cursor:'2',messages:[user(r.request,{accepted_request_id:'another'})]}),f.state.epoch));
  assert.equal(beginSend(f.state,'blocked',{allowRunning:true}),null,'an unrelated snapshot does not acknowledge a send');
  setDraft(f.state,'new draft');setDraftReferences(f.state,[{message_id:'new-reference'}]);
  assert(acceptSnapshot(f.state,snapshot({cursor:'3',messages:[user(r.request)]}),f.state.epoch));f.manager.confirmed(r);
  assert.equal(f.state.pending.size,0,'persisted matching snapshot unlocks while HTTP is held');
  assert.equal(draft(f.state),'new draft');assert.equal(draftReferences(f.state)[0].message_id,'new-reference');
  const next=f.start();assert.notEqual(next.request.request_id,r.request.request_id,'identical text can be another intentional send');
  f.calls[0].resolve({message_id:'old',duplicate:false});await flush();
  assert.equal(f.state.pending.get(next.key),next,'a late receipt cannot settle the following attempt');
  assert.equal(f.accepted.length,1,'submitted attachments are cleared only once');
  f.manager.close();await flush();assert.equal(f.clock.timers.size,0);
}
{
  const f=fixture(),r=f.start('',[{id:'upload',name:'old.pdf',mime_type:'application/pdf',size:12}]);
  f.queryState='accepted';await f.clock.advance(11);
  assert.equal(f.state.pending.size,0,'After submit timeout, receipt query unlocks without a transcript message');
  assert.equal(f.accepted.length,1);assert.equal(f.calls.length,1,'confirmation never repeats uploads or sends');
  f.manager.close();
}
{
  const f=fixture(),r=f.start();
  f.queryError=true;await f.clock.advance(40);
  assert.equal(f.state.pending.get(r.key),r);assert.equal(r.phase,'uncertain');assert.equal(r.recoverable,true);
  assert.equal(f.queries.length,3,'automatic queries are bounded and use backoff');
  setDraft(f.state,'new draft');f.scope={space:'other-space',owner:'b'};
  const epoch=beginSubscription(f.state);acceptSnapshot(f.state,snapshot({owner_id:'other',boot_id:'new-boot'}),epoch);
  f.queryError=false;
  const recovery=f.manager.recover(r);await flush();
  assert.equal(f.calls.length,2);assert.equal(f.calls[1].request,r.request,'retry uses the fixed original payload and request id');
  assert.equal(f.calls[1].request.user_message_id,'original','recovery never retargets a new turn');
  assert.equal(f.calls[1].options.space,'space');assert.equal(f.queries.at(-1).options.owner,'a');
  f.calls[1].resolve({message_id:'accepted',duplicate:true});await recovery;
  assert.equal(f.state.pending.size,0);assert.equal(f.state.drafts.get(r.key),'new draft');
  f.manager.close();
}
{
  const f=fixture(),r=f.start();
  await f.clock.advance(40);f.queryState='accepted';await f.manager.recover(r);
  assert.equal(f.calls.length,1,'accepted after timeout is not reposted');assert.equal(f.accepted.length,1);f.manager.close();
}
{
  const f=fixture(),r=f.start();
  await f.clock.advance(40);f.queryState='rejected';await f.manager.recover(r);
  assert.equal(f.state.pending.size,0,'closed original turn can definitively reject an absent steer');
  assert.equal(f.calls.length,1);assert.equal(draft(f.state),'same text');assert.equal(f.rejected.length,1);
  acceptSnapshot(f.state,snapshot({cursor:'2',running:false,turn_id:null}),f.state.epoch);
  const next=beginSend(f.state,'explicit-new-send',{allowRunning:true});
  assert.equal(next.request.request_id,'explicit-new-send','after definitive rejection, an explicit send can target the current conversation');
  assert.equal(next.request.user_message_id,undefined);f.manager.close();
}
{
  const f=fixture();f.client.sendReceipt=async request=>({request_id:request.request_id,conversation_id:'main',user_message_id:'original',runtime_id:'another-control',owner_id:'a',state:'accepted',message_id:'wrong'});
  const r=f.start();await f.clock.advance(40);
  assert.equal(f.state.pending.get(r.key),r,'a receipt from a different Control cannot unlock');f.manager.close();
}
{
  const f=fixture(),r=f.start();setDraft(f.state,'changed');setDraft(f.state,r.request.text);
  f.calls[0].resolve({message_id:'accepted',duplicate:false});await flush();
  assert.equal(draft(f.state),r.request.text,'editing away and back creates a new draft intent');f.manager.close();
}
{
  const f=fixture();f.sendError={status:400};f.start();await flush();
  assert.equal(f.state.pending.size,0);assert.equal(f.rejected.length,1);assert.equal(draft(f.state),'same text');assert.equal(f.queries.length,0);f.manager.close();
}
{
  const f=fixture(),r=f.start();await f.clock.advance(40);f.sendError={status:400};await f.manager.recover(r);
  assert.equal(f.state.pending.get(r.key),r,'a failed retry cannot disprove an unknown earlier acceptance');f.manager.close();
}
{
  const f=fixture(),r=f.start();
  const epoch=beginSubscription(f.state);acceptSnapshot(f.state,snapshot({boot_id:'next-boot'}),epoch);
  assert.equal(beginSend(f.state,'new-id',{allowRunning:true}),null,'boot change retains the unknown original attempt');
  f.queryState='accepted';await f.clock.advance(11);assert.equal(f.state.pending.size,0);f.manager.close();
}
{
  const f=fixture();
  acceptSnapshot(f.state,snapshot({kind:'reset',cursor:'2',_latest:'100',history:{has_newer:true},messages:[{id:'older',role:'user',text:'old page',status:'complete',sequence:'1',version:1}]}),f.state.epoch);
  const r=f.start();
  acceptSnapshot(f.state,snapshot({kind:'delta',base_cursor:2,cursor:'3',messages:[user(r.request,{sequence:'90',version:3})]}),f.state.epoch);f.manager.confirmed(r);
  assert.equal(f.state.pending.size,0,'delta acceptance survives history-window filtering');
  assert(!f.state.snapshot.messages.some(m=>m.id===r.request.request_id),'acknowledging row need not enter the visible cache');
  assert.equal(f.accepted.length,1);f.manager.close();
}
{
  const f=fixture(),attachments=[{id:'original',name:'old.pdf'}];
  setDraftReferences(f.state,[{message_id:'ref',quote:'original'}]);const r=f.start('fixed',attachments);
  attachments[0].id='new';setDraftReferences(f.state,[{message_id:'other'}]);
  assert.equal(r.request.attachments[0].id,'original');assert.equal(r.request.references[0].message_id,'ref');
  f.manager.close();
}
{
  const state=createConversationState();beginSubscription(state);
  acceptSnapshot(state,snapshot({running:false,turn_id:null}),state.epoch);setDraft(state,'rejected draft');
  const first=beginSend(state,'original',{retryUserMessageID:null});settleSend(state,first,false,{status:409});
  const retry=beginSend(state,'new-id',{retryUserMessageID:null});
  assert.equal(retry.request,first.request,'the composer passes null for an absent retry target; rejection retry retains its request');
}
{
  const f=fixture(),r=f.start();f.queryError=true;await f.clock.advance(40);
  assert.equal(r.recoverable,true);assert.equal(f.queries.length,3);
  f.queryError=false;f.queryState='accepted';await f.manager.resume();
  assert.equal(f.state.pending.size,0,'Foreground/network recovery confirms an exhausted attempt without a click');
  assert.equal(draft(f.state),'');assert.equal(f.calls.length,1,'Automatic recovery is read-only');assert.equal(f.queries.length,4);assert.equal(f.accepted.length,1);f.manager.close();
}
const receiptValue=(request,state='accepted')=>({request_id:request.request_id,conversation_id:request.conversation_id,user_message_id:request.user_message_id,retry_user_message_id:request.retry_user_message_id,runtime_id:'control',owner_id:'a',state,...(state==='accepted'?{message_id:'accepted'}:{})});
{
  const f=fixture(),r=f.start();await f.clock.advance(40);
  await Promise.all([f.manager.resume(),f.manager.resume(),f.manager.resume()]);
  assert.equal(f.queries.length,4,'A recovery burst issues one read');assert.equal(r.recoverable,true);assert.equal(f.calls.length,1);
  await f.clock.advance(500);await f.manager.resume();assert.equal(f.queries.length,4,'Repeated signals respect the cooldown');
  await f.clock.advance(500);await f.manager.resume();assert.equal(f.queries.length,5);
  f.queryError=true;await f.clock.advance(1000);await f.manager.resume();assert.equal(f.queries.length,6);assert.equal(f.state.pending.get(r.key),r);
  await f.clock.advance(100000);assert.equal(f.queries.length,6,'Unknown results never restart periodic polling');assert.equal(f.clock.timers.size,0);f.manager.close();
}
{
  const f=fixture(),r=f.start();f.queryError=true;await f.clock.advance(40);let resolve;const reads=[];
  f.client.sendReceipt=(request,options)=>new Promise(yes=>{reads.push({request,options});resolve=yes;});
  const auto=f.manager.resume();await flush();const manual=f.manager.recover(r),again=f.manager.recover(r);
  assert.equal(reads.length,1,'Unknown-result recovery signals share the active check');assert.equal(f.calls.length,1);
  resolve(receiptValue(r.request));await Promise.all([auto,manual,again]);await f.clock.advance(100000);
  assert.equal(reads.length,1);assert.equal(f.accepted.length,1);assert.equal(f.calls.length,1);f.manager.close();
}
{
  const f=fixture();setDraftReferences(f.state,[{message_id:'submitted'}]);const r=f.start();f.queryError=true;await f.clock.advance(40);
  const reads=[];f.client.sendReceipt=(request,options)=>new Promise(resolve=>reads.push({request,options,resolve}));
  const old=f.manager.resume();await flush();assert.equal(reads.length,1);
  setDraft(f.state,'New alpha draft');setDraftReferences(f.state,[{message_id:'new-alpha'}]);
  f.scope={space:'space',owner:'b'};const epoch=beginSubscription(f.state);acceptSnapshot(f.state,snapshot({owner_id:'["space","b"]'}),epoch);setDraft(f.state,'New beta draft');setDraftReferences(f.state,[{message_id:'new-beta'}]);
  f.online=false;await f.manager.resume();assert(reads[0].options.signal.aborted,'Foreground detecting offline cancels the actual query signal');
  f.online=true;const fresh=f.manager.resume();await flush();assert.equal(reads.length,2,'Resume does not wait for the aborted promise');
  reads[0].resolve(receiptValue(r.request));await old;await flush();await f.manager.resume();
  assert.equal(reads.length,2);assert.equal(f.state.pending.get(r.key),r,'A late cancelled result cannot settle or release the newer query');assert(!reads[1].options.signal.aborted);
  reads[1].resolve(receiptValue(r.request));await fresh;
  assert.equal(f.state.pending.size,0);assert.equal(f.state.drafts.get(r.key),'New alpha draft');assert.equal(f.state.referenceDrafts.get(r.key)[0].message_id,'new-alpha');assert.equal(draft(f.state),'New beta draft');assert.equal(draftReferences(f.state)[0].message_id,'new-beta');
  assert(reads.every(read=>read.options.owner==='a'&&read.options.space==='space'&&read.request===r.request));assert.equal(f.accepted.length,1);assert.equal(f.calls.length,1);f.manager.close();
}
{
  const f=fixture(),r=f.start();f.queryError=true;await f.clock.advance(40);
  const reads=[];f.client.sendReceipt=(request,options)=>new Promise(resolve=>{reads.push({options,resolve});options.signal.addEventListener('abort',()=>resolve(receiptValue(request)));});
  const check=f.manager.resume();await flush();await f.clock.advance(11);await check;assert(reads[0].options.signal.aborted);assert.equal(r.recoverable,true);
  reads[0].resolve(receiptValue(r.request));await flush();assert.equal(f.state.pending.get(r.key),r,'A timed-out read cannot acknowledge later');
  await f.clock.advance(10000);assert.equal(reads.length,1);assert.equal(f.clock.timers.size,0);
  f.client.sendReceipt=async request=>receiptValue(request);await f.manager.resume();assert.equal(f.accepted.length,1);f.manager.close();
}
{
  const f=fixture();f.queryError=true;f.start();await f.clock.advance(40);let reads=0;
  f.client.sendReceipt=async request=>{reads++;return receiptValue(request);};const check=f.manager.resume();f.manager.close();await check;await f.clock.advance(100000);
  assert.equal(reads,0,'Disposal before the query microtask starts prevents IO');assert.equal(f.accepted.length,0);assert.equal(f.clock.timers.size,0);
}
{
  const f=fixture(),r=f.start();f.queryError=true;await f.clock.advance(40);let signal,resolve;
  f.client.sendReceipt=(request,options)=>{signal=options.signal;return new Promise(yes=>resolve=yes);};const check=f.manager.resume();await flush();
  acceptSnapshot(f.state,snapshot({cursor:'2',messages:[user(r.request)]}),f.state.epoch);f.manager.confirmed(r);assert(signal.aborted);
  resolve(receiptValue(r.request));f.calls[0].resolve({message_id:'accepted',duplicate:false});await check;await flush();
  assert.equal(f.accepted.length,1,'SSE, a late HTTP ack and the aborted check clear submitted attachments once');assert.equal(f.clock.timers.size,0);f.manager.close();
}
{
  const f=fixture();let reads=0;f.client.sendReceipt=async()=>{reads++;throw {status:403};};const r=f.start();await f.clock.advance(40);
  for(let i=0;i<3;i++){await f.manager.resume();f.manager.suspend();await f.manager.resume();await f.clock.advance(2000);}
  assert.equal(reads,1,'An authorization denial pauses automatic checks across recovery signals');assert.equal(f.state.pending.get(r.key),r);assert.equal(f.rejected.length,0,'A receipt access denial does not reject the message');
  f.client.sendReceipt=async request=>receiptValue(request);await f.manager.recover(r);assert.equal(f.accepted.length,1);assert.equal(f.calls.length,1);f.manager.close();
}
// Exercise the actual production transport and resource command writer. The
// submit deadline ends at valid HTTP acceptance, even if local storage hangs.
for(const route of ['message','steer','retry'])for(const newerDraft of [false,true]){
  const savedFetch=globalThis.fetch,calls=[],commands=[],invalidations=[],failures=[],accepted=[];
  let resolveCommand,rejectCommand,fail=false;
  const storage={subscribe:()=>()=>{},close:()=>{},invalidate:async()=>{},
    command:async(key,meta,value)=>{commands.push({key,meta,value});if(fail)return value;return new Promise((resolve,reject)=>{resolveCommand=()=>resolve(value);rejectCommand=reject;});}};
  globalThis.fetch=async(url,options={})=>{
    const path=new URL(url).pathname;calls.push({path,method:options.method||'GET',body:options.body});
    return Response.json(path==='/api/v1/runtime'?{runtime_id:'runtime'}:{message_id:'accepted',duplicate:false});
  };
  let client,manager;
  try{
    client=await createProductionClient({base:'http://synthetic.invalid',resourceStorage:storage});client.setScope({space:'space',owner:'a'});
    client.resources.subscribeInvalidation((topics,scope)=>invalidations.push({topics,scope}));
    const state=createConversationState(),clock=new Clock();beginSubscription(state);
    acceptSnapshot(state,snapshot({running:route==='steer',turn_id:route==='steer'?'original':null}),state.epoch);
    setDraft(state,'submitted');setDraftReferences(state,[{message_id:'old-reference'}]);
    const receipt=beginSend(state,'fixed-'+route,{allowRunning:true,attachments:[{id:'old-attachment'}],retryUserMessageID:route==='retry'?'failed-input':undefined});
    manager=createSendConfirmation({state,client,clock,now:()=>clock.now,changed:()=>{},accepted:r=>accepted.push(r),cacheFailed:(...args)=>failures.push(args)});
    manager.start(receipt);
    if(newerDraft){setDraft(state,'new draft');setDraftReferences(state,[{message_id:'new-reference'}]);}
    // Response.json() uses native microtasks in addition to the manager clock.
    await new Promise(resolve=>setImmediate(resolve));await flush();
    assert.equal(accepted.length,1,'Valid HTTP success confirms before storage.command resolves');
    assert.equal(state.pending.size,0);assert.equal(draft(state),newerDraft?'new draft':'');
    assert.equal(draftReferences(state)[0]?.message_id,newerDraft?'new-reference':undefined);
    assert.equal(accepted[0].request.attachments[0].id,'old-attachment');
    await clock.advance(30000);await manager.resume();
    assert.equal(calls.filter(call=>call.path==='/api/v1/messages/receipt').length,0,'Held local saving beyond the submit timeout must not read receipts');
    assert.equal(calls.filter(call=>call.method==='POST').length,1);
    assert.equal(commands.length,1);assert.equal(invalidations.length,0,'No uncommitted local projection is published');
    if(newerDraft){
      rejectCommand(Object.assign(Error('synthetic local quota failure'),{storage:true}));await flush();
      assert.equal(failures.length,1);assert.equal(failures[0][0],receipt);assert.equal(failures[0][1].accepted,true);assert.equal(failures[0][1].uiKey,'storage.commandAccepted');
      fail=true;await failures[0][2]();await flush();
      assert.equal(commands.length,2);assert.equal(commands[1].key,commands[0].key,'Local save retry retains the original command ID');
    }else{resolveCommand();await flush();}
    assert.equal(invalidations.length,1,'Local commit still invalidates conversation and directory');
    assert.deepEqual(invalidations[0].topics,['conversation','assistants']);
    assert.equal(calls.filter(call=>call.method==='POST').length,1,'Local commit retry does not repeat the accepted POST');
    assert.equal(accepted.length,1);assert.equal(state.pending.size,0);assert.equal(draft(state),newerDraft?'new draft':'');
  }finally{resolveCommand?.();manager?.close();client?.resources.clear();globalThis.fetch=savedFetch;}
}
console.log('Send confirmation: response-first production acceptance independent of local saving; zero normal receipt reads; unknown-only recovery, cancellation, immutable scope and draft/reference fences passed');
