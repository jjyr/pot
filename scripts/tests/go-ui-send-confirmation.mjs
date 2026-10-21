import assert from 'node:assert/strict';
import {createConversationState,beginSubscription,acceptSnapshot,beginSend,settleSend,setDraft,draft,setDraftReferences,draftReferences,ownerKey} from '../../web/src/state/conversation.mjs';
import {createSendConfirmation} from '../../web/src/state/send-confirmation.mjs';

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
  let queryState='unconfirmed',scope={space:'space',owner:'a'},queryError=false,sendError;
  const client={production:true,runtime:{runtime_id:'runtime'},spaces:[{id:'space',control_id:'control'}],scope:()=>scope,
    send:(request,options)=>new Promise((resolve,reject)=>{calls.push({request,options,resolve,reject});if(sendError)reject(sendError);}),
    sendReceipt:async(request,options)=>{queries.push({request,options});if(queryError)throw Error('Offline');return {request_id:request.request_id,conversation_id:request.conversation_id,user_message_id:request.user_message_id,retry_user_message_id:request.retry_user_message_id,runtime_id:'control',owner_id:'a',state:queryState,...(queryState==='accepted'?{message_id:'accepted'}:{})};},
  };
  const manager=createSendConfirmation({state,client,clock,timeout:10,delays:[1,2,4],changed:()=>{},accepted:r=>accepted.push(r),rejected:r=>rejected.push(r)});
  const start=(text='same text',attachments=[])=>{setDraft(state,text);const r=beginSend(state,'id-'+calls.length,{allowRunning:true,attachments});manager.start(r);return r;};
  return {state,clock,calls,queries,accepted,rejected,manager,client,start,set queryState(v){queryState=v;},set queryError(v){queryError=v;},set scope(v){scope=v;},set sendError(v){sendError=v;}};
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
  f.queryState='accepted';await f.clock.advance(1);
  assert.equal(f.state.pending.size,0,'receipt query unlocks without any transcript message in the window');
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
  assert.equal(f.state.pending.size,0);assert.equal(f.rejected.length,1);assert.equal(draft(f.state),'same text');f.manager.close();
}
{
  const f=fixture(),r=f.start();await f.clock.advance(40);f.sendError={status:400};await f.manager.recover(r);
  assert.equal(f.state.pending.get(r.key),r,'a failed retry cannot disprove an unknown earlier acceptance');f.manager.close();
}
{
  const f=fixture(),r=f.start();
  const epoch=beginSubscription(f.state);acceptSnapshot(f.state,snapshot({boot_id:'next-boot'}),epoch);
  assert.equal(beginSend(f.state,'new-id',{allowRunning:true}),null,'boot change retains the unknown original attempt');
  f.queryState='accepted';await f.clock.advance(1);assert.equal(f.state.pending.size,0);f.manager.close();
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
console.log('Send confirmation: held receipts, snapshot/query acceptance, bounded unknown recovery, immutable retries, identity fences and drafts passed');
