import {test} from 'node:test';
import assert from 'node:assert/strict';
import {poll} from './notifications.mjs';
const start=Date.parse('2026-01-01T00:00:00Z'),date=n=>new Date(start+n*1000).toISOString();
process.env.GH_TOKEN='fixture-token';
const filters=(repositories=null,reasons=null)=>({repositories,reasons,subject_types:null,threads:null});
const sub=(id,assistant,repositories=null,reasons=null)=>({id,assistant,enabled:true,account_id:'123',changed_at:'',revision:1,filters:filters(repositories,reasons)});
const thread=(id,repo,reason,at=80)=>({id,updated_at:date(at),reason,repository:{full_name:repo},subject:{type:'Release',title:'Update '+id,url:'https://api.github.com/repos/'+repo+'/releases/1'}});
function harness(subscriptions){let rows=[],requests=0;const run=async(now,state=null,subs=subscriptions)=>poll({now:start+now*1000,accounts:{github:'123'},config:{initialized:true,subscriptions:subs},state},{fetch:async(url,options)=>{assert.equal(options.method,'GET');assert.equal(url.pathname,'/notifications');requests++;return Response.json(rows);}});return {run,set(value){rows=value;},get requests(){return requests;}};}
test('shared account scan fans out B/C/default, overlapping subscriptions dedup per assistant',async()=>{
 const subs=[sub('default','A',null,['mention']),sub('fiber-B','B',['nervosnetwork/fiber']),sub('fiber-B-2','B',['nervosnetwork/fiber']),sub('pot-C','C',['jjyr/pot','jjyr/personal-one','jjyr/personal-two'])];
 const h=harness(subs);let r=await h.run(0);
 h.set([thread('1','nervosnetwork/fiber','subscribed'),thread('2','jjyr/pot','mention'),thread('3','jjyr/personal-two','comment'),thread('4','else/unrelated','subscribed')]);r=await h.run(90,r.state);assert.equal(r.events.length,0);assert.equal(Object.keys(r.state.pending).length,4);
 r=await h.run(180,r.state);assert.equal(h.requests,3);assert.equal(r.events.length,4);assert.equal(r.events.filter(e=>e.assistant==='B').length,1);assert.equal(r.events.filter(e=>e.assistant==='C').length,2);assert.equal(r.events.filter(e=>e.assistant==='A').length,1);
 r=await h.run(270,structuredClone(r.state));assert.equal(r.events.length,0);
});
test('same snapshot matching different assistants goes once to each, detail expansion only once',async()=>{
 const subs=[sub('B','B'),sub('C','C')],h=harness(subs);let r=await h.run(0);let details=0;
 const row=thread('1','jjyr/pot','mention');row.subject={...row.subject,type:'Issue',url:'https://api.github.com/repos/jjyr/pot/issues/1'};
 r=await poll({now:start+90000,state:r.state,accounts:{github:'123'},config:{initialized:true,subscriptions:subs}},{fetch:async url=>{if(url.pathname==='/notifications')return Response.json([row]);details++;return url.pathname.endsWith('/comments')?Response.json([]):Response.json({});}});
 assert.equal(details,2);h.set([row]);r=await h.run(180,r.state);assert.deepEqual(r.events.map(e=>e.assistant).sort(),['B','C']);
});
test('pause/tighten/delete and retarget discard only affected pending routes; C retains its update',async()=>{
 for(const change of ['pause','delete','retarget','tighten']){
 const original=[sub('B','B'),sub('C','C')],h=harness(original);let r=await h.run(0);h.set([thread('1','jjyr/pot','subscribed')]);r=await h.run(90,r.state);
 const changed=structuredClone(original);changed[0].changed_at=date(100);
 if(change==='pause')changed[0].enabled=false;if(change==='delete')changed.shift();if(change==='retarget')changed[0].assistant='C';if(change==='tighten')changed[0].filters.reasons=['mention'];
 r=await h.run(180,r.state,changed);assert.deepEqual(r.events.map(e=>e.assistant),['C'],change);
 const wider=structuredClone(original);wider[0].changed_at=date(200);r=await h.run(270,r.state,wider);assert.equal(r.events.length,0,change+' replays history');
 }
});
test('removing one overlap preserves the other subscription and unknown account gets no deliveries',async()=>{
 const original=[sub('B1','B'),sub('B2','B')],h=harness(original);let r=await h.run(0);h.set([thread('1','jjyr/pot','mention')]);r=await h.run(90,r.state);r=await h.run(180,r.state,[original[1]]);assert.equal(r.events.length,1);assert.equal(r.events[0].assistant,'B');
 const wrong=original.map(s=>({...s,account_id:'another-account'}));r=await h.run(270,r.state,wrong);h.set([thread('1','jjyr/pot','mention',300)]);r=await h.run(360,r.state,wrong);assert.equal(r.events.length,0);assert.deepEqual(r.state.pending,{});
});
test('deleted assistants cannot block remaining recipients or retain pending delivery',async()=>{
 const original=[sub('B','B'),sub('C','C')],h=harness(original);let r=await h.run(0);h.set([thread('1','jjyr/pot','mention')]);r=await h.run(90,r.state);
 r=await poll({now:start+180000,state:r.state,accounts:{github:'123'},assistants:['C'],config:{initialized:true,subscriptions:original}},{fetch:async()=>Response.json([])});
 assert.deepEqual(r.events.map(e=>e.assistant),['C']);assert.deepEqual(r.state.pending,{});
});

// Simultaneous edits of different subscriptions still CAS the same opaque root.
test('collection CAS prevents lost concurrent edits and fresh retry preserves both',async()=>{
 const {subscriptionCall}=await import('./subscriptions.mjs');
 let root={id:'github',revision:1,config:{initialized:true,subscriptions:[sub('B','B'),sub('C','C')]}},waiting=[],writes=0;
 const receipts=new Map();
 const host=async(method,input)=>{
  if(method==='pot/context')return {accounts:{github:'123'},assistants:[{id:'B'},{id:'C'}]};
  if(method==='pot/background/list')return {items:[structuredClone(root)]};
  if(method==='pot/background/receipt'){const prior=receipts.get(input.request_id);if(prior&&prior!==input.input_hash)throw Error('notification_request_conflict');return {applied:!!prior};}
  if(method==='pot/background/write'){
   if(++writes<=2)await new Promise(resolve=>{waiting.push(resolve);if(waiting.length===2)waiting.forEach(f=>f());});
   if(input.revision!==root.revision)throw Error('stale plugin configuration');
   root={...root,revision:root.revision+1,config:structuredClone(input.config)};receipts.set(input.request_id,input.input_hash);return {saved:true};
  }
  throw Error('unexpected callback');
 };
 const update=id=>({id,revision:1,request_id:'edit-'+id,account_id:'123',assistant:id,enabled:false,filters:filters()});
 const results=await Promise.allSettled(['B','C'].map(id=>subscriptionCall(host,'update_subscription',update(id))));
 assert.equal(results.filter(r=>r.status==='fulfilled').length,1);assert.equal(results.filter(r=>r.status==='rejected').length,1);
 const loser=['B','C'][results.findIndex(r=>r.status==='rejected')];await subscriptionCall(host,'update_subscription',update(loser));assert(root.config.subscriptions.every(s=>!s.enabled));assert.equal(root.revision,3);
});
