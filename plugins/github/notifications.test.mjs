import {test} from 'node:test';
import assert from 'node:assert/strict';
import {poll} from './notifications.mjs';
process.env.GH_TOKEN='fake-token';
const start=Date.parse('2026-01-01T10:00:00Z');
const date=n=>new Date(start+n*1000).toISOString();
const thread=(n,reason='mention')=>({id:'t1',updated_at:date(n),reason,repository:{full_name:'me/repo'},subject:{title:'Review this',type:'Issue',url:'https://api.github.com/repos/me/repo/issues/1',latest_comment_url:'https://api.github.com/repos/me/repo/issues/comments/'+n}});
const comment=n=>({id:n,body:'comment-'+n,created_at:date(n),updated_at:date(n),user:{login:'alice'},html_url:'https://github.com/me/repo/issues/1#issuecomment-'+n});
function harness(){
 let rows=[thread(-1)],comments=[],code=200,headers={'X-Poll-Interval':'90','Last-Modified':'Thu, 01 Jan 2026 10:00:00 GMT'},calls=[];
 const fake=async(url,init)=>{calls.push({url:String(url),...init});assert.equal(init.method,'GET');assert.equal(init.headers.Authorization,'Bearer fake-token');if(url.pathname.endsWith('/notifications'))return new Response(code===304?null:JSON.stringify(rows),{status:code,headers});if(url.pathname.endsWith('/comments'))return Response.json(comments);return Response.json({body:'Issue context',user:{login:'me'}});};
 const run=(now,state=null,config={reasons:null})=>poll({now:start+now*1000,state,config,accounts:{github:'123'}},{fetch:fake});
 return {run,calls,set(value){if(value.rows)rows=value.rows;if(value.comments)comments=value.comments;if(value.code)code=value.code;if(value.headers)headers=value.headers;}};
}
test('baseline, overlapping cursor, incremental comments, coalescing, 304 and restart',async()=>{
 const h=harness();let r=await h.run(0);assert.equal(r.status,'baseline');assert.deepEqual(r.events,[]);assert.equal(h.calls.length,1);assert.equal(r.delay_seconds,90);
 h.set({rows:[thread(80)],comments:[comment(-1),comment(80)]});r=await h.run(90,r.state);assert.equal(r.events.length,0);assert.match(h.calls[1].url,/since=/);assert.ok(h.calls[1].headers['If-Modified-Since']);
 h.set({rows:[thread(160)],comments:[comment(80),comment(160)]});r=await h.run(180,JSON.parse(JSON.stringify(r.state)));assert.equal(r.events.length,0);
 h.set({code:304});r=await h.run(270,r.state);assert.equal(r.events.length,1);assert.match(r.events[0].body,/comment-160/);assert.match(r.events[0].body,/comment-80/);assert.doesNotMatch(r.events[0].body,/comment--1/);
 r=await h.run(360,r.state);assert.equal(r.events.length,0);
});
test('filters establish progress without model events; account switch establishes a fresh baseline',async()=>{
 const h=harness();let r=await h.run(0);h.set({rows:[thread(80)]});r=await h.run(90,r.state,{repositories:['other/repo']});assert.deepEqual(r.events,[]);assert.equal(h.calls.length,2);assert.match(h.calls[1].url,/\/notifications/);
 r=await h.run(180,{...r.state,account:'other'});assert.equal(r.status,'baseline');assert.equal(r.events.length,0);
});
test('permission, expiration, rate limits and network failures preserve cursor and back off',async()=>{
 for(const [code,headers,status]of [[403,{},'permission'],[401,{},'authorization'],[429,{'retry-after':'600'},'rate_limited'],[403,{'x-ratelimit-remaining':'0','x-ratelimit-reset':String((start+900000)/1000)},'rate_limited']]){
 const h=harness();const initial=await h.run(0);h.set({code,headers});const r=await h.run(90,initial.state);assert.equal(r.status,status);assert.equal(r.state.since,initial.state.since);assert.equal(r.events.length,0);assert.ok(r.delay_seconds>=60);
 }
 const h=harness(),initial=await h.run(0);let r=await poll({now:start+90000,state:initial.state,config:{reasons:null},accounts:{github:'123'}},{fetch:async()=>{throw Error('offline');}});assert.equal(r.status,'backoff');assert.equal(r.state.since,initial.state.since);
});
test('pagination advances only after complete fetch; unsafe links never receive credentials',async()=>{
 let calls=0;
 const fetcher=async(url)=>{calls++;if(url.searchParams.get('page')==='2')return Response.json([thread(-2)]);return Response.json([thread(-1)],{headers:{link:'<https://api.github.com/notifications?page=2>; rel="next"'}});};
 let r=await poll({now:start,state:null,config:{reasons:null},accounts:{github:'123'}},{fetch:fetcher});assert.equal(r.status,'baseline');assert.equal(calls,2);assert.equal(r.state.modified,null);
 calls=0;r=await poll({now:start,state:null,config:{reasons:null},accounts:{github:'123'}},{fetch:async()=>{calls++;return Response.json([thread(-1)],{headers:{link:'<https://evil.test/notifications?page=2>; rel="next"'}});}});assert.equal(r.status,'permission');assert.equal(calls,1);assert.equal(r.state.baseline,false);
});
test('later mention with no comment remains a reason update; duplicate snapshots produce no new event',async()=>{
 const h=harness();let r=await h.run(0);h.set({rows:[thread(80,'review_requested')]});r=await h.run(90,r.state);r=await h.run(180,r.state);assert.equal(r.events.length,1);assert.match(r.events[0].body,/review_requested/);r=await h.run(270,r.state);assert.equal(r.events.length,0);
});
test('PR reviews and review comments are expanded; same-second new comments survive the boundary',async()=>{
 let rows=[thread(0)],responses=0;
 rows[0].subject.latest_comment_url='https://api.github.com/repos/me/repo/issues/comments/1';
 const initial=await poll({now:start,state:null,config:{reasons:null},accounts:{github:'123'}},{fetch:async()=>Response.json(rows)});
 rows=[thread(0)];rows[0].subject.latest_comment_url='https://api.github.com/repos/me/repo/issues/comments/2';
 let r=await poll({now:start+90000,state:initial.state,config:{reasons:null},accounts:{github:'123'}},{fetch:async url=>url.pathname==='/notifications'?Response.json(rows):url.pathname.endsWith('/comments')?Response.json([{...comment(0),id:1,body:'old'},{...comment(0),id:2,body:'new boundary comment'}]):Response.json({})});
 r=await poll({now:start+180000,state:r.state,config:{reasons:null},accounts:{github:'123'}},{fetch:async()=>new Response(null,{status:304})});assert.equal(r.events.length,1);assert.match(r.events[0].body,/new boundary comment/);assert.doesNotMatch(r.events[0].body,/"body":"old"/);
 const pr=thread(80,'review_requested');pr.subject.type='PullRequest';pr.subject.url='https://api.github.com/repos/me/repo/pulls/1';
 r=await poll({now:start+270000,state:initial.state,config:{reasons:null},accounts:{github:'123'}},{fetch:async url=>{
 responses++;if(url.pathname==='/notifications')return Response.json([pr]);if(url.pathname.endsWith('/reviews'))return Response.json([{id:3,body:'review submitted',submitted_at:date(80)}]);if(url.pathname.endsWith('/comments'))return Response.json([comment(80)]);return Response.json({});
 }});assert.equal(responses,5);
 r=await poll({now:start+360000,state:r.state,config:{reasons:null},accounts:{github:'123'}},{fetch:async()=>new Response(null,{status:304})});assert.equal(r.events.length,1);assert.match(r.events[0].body,/review submitted/);assert.match(r.events[0].body,/review_comment/);
});
test('bounded Unicode titles and large escaped comment bodies remain valid events',async()=>{
 const h=harness();let r=await h.run(0);const next=thread(80);next.subject.title='通知'.repeat(200);h.set({rows:[next],comments:Array.from({length:40},(_,i)=>({...comment(80+i),body:'\u0001'.repeat(3000)}))});r=await h.run(90,r.state);h.set({code:304});r=await h.run(180,r.state);assert.equal(r.events.length,1);assert.ok(Buffer.byteLength(r.events[0].title)<=512);assert.ok(Buffer.byteLength(r.events[0].body)<=24000);assert.match(r.events[0].body,/"truncated":true/);
});
test('poll and retry intervals longer than a host wake remain respected across process restarts',async()=>{
 for(const [status,headers]of [[200,{'x-poll-interval':'172800'}],[429,{'retry-after':'172800'}]]){
 let calls=0;const fake=async()=>{calls++;return Response.json([],{status,headers});};
 let r=await poll({now:start,state:null,config:{reasons:null},accounts:{github:'123'}},{fetch:fake});assert.equal(r.delay_seconds,86400);
 r=await poll({now:start+86400000,state:JSON.parse(JSON.stringify(r.state)),config:{reasons:null},accounts:{github:'123'}},{fetch:fake});assert.equal(calls,1);assert.equal(r.events.length,0);
 await poll({now:start+172800000,state:r.state,config:{reasons:null},accounts:{github:'123'}},{fetch:fake});assert.equal(calls,2);
 }
});

test('emission pacing is checkpointed across threads and restart without losing pending updates',async()=>{
 const h=harness();let r=await h.run(0);
 h.set({rows:[thread(80)],comments:[comment(80)]});r=await h.run(90,r.state);
 h.set({code:304});r=await h.run(180,r.state);assert.equal(r.events.length,1);
 assert.equal(r.state.nextEmitAt,start+480000);assert.equal('group' in r.events[0],false);
 const next={...thread(200),id:'t2',subject:{...thread(200).subject,url:'https://api.github.com/repos/me/repo/issues/2'}};
 h.set({code:200,rows:[next],comments:[comment(200)]});r=await h.run(270,r.state);
 h.set({code:304});r=await h.run(360,JSON.parse(JSON.stringify(r.state)));assert.equal(r.events.length,0);assert.ok(r.state.pending['me/repo#2']);
 r=await h.run(479,r.state);assert.equal(r.events.length,0);
 r=await h.run(480,JSON.parse(JSON.stringify(r.state)));assert.equal(r.events.length,1);assert.match(r.events[0].url,/issues\/2$/);assert.deepEqual(r.state.pending,{});
 r=await h.run(540,r.state);assert.equal(r.events.length,0);
});
test('explicit all-reasons selection retains broad ingestion regression coverage',async()=>{
 for(const reason of ['assign','ci_activity','security_alert']){
  const h=harness();let r=await h.run(0);h.set({rows:[thread(80,reason)],comments:[comment(80)]});
  r=await h.run(90,r.state,{reasons:[reason]});assert.equal(r.status,'ready');assert.ok(Object.keys(r.state.pending).length,reason);
 }
 const h=harness();let r=await h.run(0);h.set({rows:[thread(80,'assign')],comments:[comment(80)]});
 r=await h.run(90,r.state);assert.ok(Object.keys(r.state.pending).length,'Assignments are included by default');
 r=await h.run(180,r.state,{reasons:['invalid reason']});assert.equal(r.status,'overflow');
});

test('all GitHub subject types produce linked snapshots without optional detail APIs',async()=>{
 for(const type of ['CheckSuite','Release','Discussion','RepositoryVulnerabilityAlert','FutureNotification']){
  const h=harness();let r=await h.run(0);const next=thread(80,'ci_activity');next.subject={title:'Update',type,url:'https://api.github.com/repos/me/repo/check-suites/1'};
  h.set({rows:[next]});r=await h.run(90,r.state);assert.equal(h.calls.length,2);
  h.set({code:304});r=await h.run(180,r.state);assert.equal(r.events.length,1,type);assert.match(r.events[0].body,new RegExp(type));assert.match(r.events[0].url,/^https:\/\/github.com\//);
 }
});
test('failed detail reads cannot roll back a notification or prevent later updates',async()=>{
 const h=harness();const baseline=await h.run(0);let calls=0;
 let r=await poll({now:start+90000,state:baseline.state,config:{reasons:null},accounts:{github:'123'}},{fetch:async url=>{
  calls++;if(url.pathname.endsWith('/notifications'))return Response.json([thread(80),{...thread(81,'assign'),id:'t2',subject:{...thread(81).subject,url:'https://api.github.com/repos/me/repo/issues/2'}}]);
  throw Error('deleted, private or temporarily offline');
 }});
 assert.equal(r.status,'ready');assert.equal(calls,3);assert.equal(Object.keys(r.state.pending).length,2);assert.equal(r.state.since,date(90));
 h.set({code:304});r=await h.run(180,r.state);assert.equal(r.events.length,2);assert.ok(r.events.every(e=>JSON.parse(e.body).updates[0].truncated));
 h.set({code:200,rows:[thread(260)],comments:[comment(260)]});r=await h.run(270,r.state);assert.equal(r.status,'ready');assert.ok(r.state.pending['me/repo#1']);
});
test('slow detail reads exhaust the shared budget without blocking snapshot progress',async()=>{
 const h=harness(),baseline=await h.run(0);let elapsed=0,calls=0;
 const rows=Array.from({length:30},(_,i)=>({...thread(80),id:'t'+i,subject:{...thread(80).subject,url:'https://api.github.com/repos/me/repo/issues/'+i}}));
 let r=await poll({now:start+90000,state:baseline.state,config:{reasons:null},accounts:{github:'123'}},{clock:()=>elapsed,fetch:async url=>{calls++;elapsed+=5000;if(url.pathname.endsWith('/notifications'))return Response.json(rows);return Response.json({});}});
 assert.equal(r.status,'ready');assert.ok(calls<=5);assert.equal(r.state.since,date(90));assert.equal(Object.keys(r.state.pending).length,30);
 h.set({code:304});r=await h.run(180,r.state);assert.equal(r.events.length,8);
});
test('large feeds checkpoint pages across restarts instead of restarting the scan',async()=>{
 let elapsed=0;const calls=[];
 const fake=async url=>{calls.push(url.searchParams.get('page')||'1');elapsed+=13000;const page=Number(url.searchParams.get('page')||1);return Response.json([{...thread(-1),id:'p'+page}],{headers:page<4?{link:'<https://api.github.com/notifications?page='+(page+1)+'>; rel="next"'}:{}});};
 let r=await poll({now:start,state:null,config:{reasons:null},accounts:{github:'123'}},{clock:()=>elapsed,fetch:fake});assert.deepEqual(calls,['1','2']);assert.equal(r.state.since,null);assert.ok(r.state.scan);
 elapsed=0;r=await poll({now:start+60000,state:JSON.parse(JSON.stringify(r.state)),config:{reasons:null},accounts:{github:'123'}},{clock:()=>elapsed,fetch:fake});assert.deepEqual(calls,['1','2','3','4']);assert.equal(r.status,'baseline');assert.equal(r.state.scan,null);assert.equal(r.state.since,date(0));assert.equal(Object.keys(r.state.threads).length,4);
});
test('previously accepted notifications drain during a feed failure',async()=>{
 const h=harness();let r=await h.run(0);h.set({rows:[thread(80)]});r=await h.run(90,r.state);h.set({code:403});r=await h.run(180,r.state);assert.equal(r.status,'permission');assert.equal(r.events.length,1);assert.equal(r.state.since,date(90));
});
test('removed history settings are rejected rather than starting an unbounded import',async()=>{
 const h=harness();const r=await h.run(0,null,{existing:'obsolete'});assert.equal(r.status,'overflow');assert.equal(h.calls.length,0);
});
test('detail rate limits preserve snapshots and delay further upstream reads',async()=>{
 const h=harness();const baseline=await h.run(0);let calls=0;
 const fake=async url=>{calls++;return url.pathname.endsWith('/notifications')?Response.json([thread(80)]):Response.json({}, {status:429,headers:{'retry-after':'600'}});};
 let r=await poll({now:start+90000,state:baseline.state,config:{reasons:null},accounts:{github:'123'}},{fetch:fake});assert.equal(r.status,'rate_limited');assert.equal(r.state.since,date(90));assert.ok(r.state.pending['me/repo#1']);
 r=await poll({now:start+180000,state:JSON.parse(JSON.stringify(r.state)),config:{reasons:null},accounts:{github:'123'}},{fetch:fake});assert.equal(calls,2);assert.equal(r.events.length,1);assert.equal(r.status,'rate_limited');
});
test('a full pending buffer resumes remaining rows without losing notifications',async()=>{
 const h=harness();const baseline=await h.run(0);let reads=0;
 const rows=Array.from({length:140},(_,i)=>({...thread(80,'ci_activity'),id:'burst'+i,subject:{title:'CI '+i,type:'CheckSuite'}}));
 const fake=async()=>{reads++;return Response.json(rows);};
 let r=await poll({now:start+90000,state:baseline.state,config:{reasons:null},accounts:{github:'123'}},{fetch:fake});assert.equal(Object.keys(r.state.pending).length,128);assert.equal(r.state.scan.rows.length,12);assert.equal(r.state.since,date(0));
 const ids=new Set();for(let t=180;t<9000;t+=90){r=await poll({now:start+t*1000,state:JSON.parse(JSON.stringify(r.state)),config:{reasons:null},accounts:{github:'123'}},{fetch:fake});for(const e of r.events){assert.ok(!ids.has(e.id));ids.add(e.id);}if(ids.size===140)break;}
 assert.equal(ids.size,140);assert.equal(r.state.scan,null);
});
test('malformed subjects fall back to basic notifications without blocking valid rows',async()=>{
 const h=harness();let r=await h.run(0);const bad=thread(80);bad.subject.url='https://[';
 h.set({rows:[null,bad,{...thread(81,'assign'),id:'valid'}]});r=await h.run(90,r.state);assert.equal(r.status,'ready');assert.equal(r.state.since,date(90));assert.equal(Object.keys(r.state.pending).length,2);
 h.set({code:304});r=await h.run(180,r.state);assert.equal(r.events.length,2);assert.ok(r.events.some(e=>e.url.startsWith('https://github.com/notifications?')));
});

test('realtime default suppresses PR1701 subscribed comment before any detail read, mention passes',async()=>{
 const h=harness();let r=await h.run(0,null,{});
 const pr={...thread(80,'subscribed'),id:'170100123',repository:{full_name:'nervosnetwork/fiber'},subject:{title:'PR 1701 ordinary comment without a mention',type:'PullRequest',url:'https://api.github.com/repos/nervosnetwork/fiber/pulls/1701'}};
 h.set({rows:[pr]});r=await h.run(90,r.state,{});assert.deepEqual(r.state.pending,{});assert.equal(h.calls.length,2);assert.deepEqual(r.events,[]);
 h.set({rows:[{...pr,updated_at:date(170),reason:'mention'}]});r=await h.run(180,r.state,{});assert.ok(Object.keys(r.state.pending).length);assert.ok(h.calls.length>3);
 for(const reason of ['comment','author','team_mention','review_requested']){const f=harness();let x=await f.run(0,null,{});f.set({rows:[thread(80,reason)]});x=await f.run(90,x.state,{});assert.deepEqual(x.state.pending,{});assert.equal(f.calls.length,2);}
});

test('repository/type/reason/thread filters use AND with OR values; null all and [] none',async()=>{
 const {matches,validateFilters,reasonSemantics}=await import('./filters.mjs');
 const row={...thread(80),id:'123456'};
 const filter={repositories:['ME/repo'],subject_types:['PullRequest','Issue'],reasons:['mention'],threads:['123456']};
 validateFilters(filter);assert(matches(row,filter));
 for(const key of Object.keys(filter)){assert(!matches(row,{...filter,[key]:[]}));assert(matches(row,{[key]:null}));}
 assert(!matches(row,{...filter,threads:['1701']}));
 for(const invalid of [{reasons:['direct_reply']},{reasons:['participating']},{subject_types:['Fake']},{threads:['me/repo#1701']},{repositories:['repo']},{author:['me']}])assert.throws(()=>validateFilters(invalid));
 assert.match(reasonSemantics,/mention can persist/);assert.match(reasonSemantics,/do not mean a direct reply/);
});

test('newly allowed threads already older than cursor do not replay history',async()=>{
 const h=harness();let r=await h.run(0,null,{});h.set({rows:[{...thread(-10,'subscribed'),id:'old-thread'}]});
 r=await h.run(90,r.state,{reasons:null});assert.deepEqual(r.state.pending,{});assert.equal(h.calls.length,2);
});

test('reenabling/broadening only emits updates after the configuration boundary',async()=>{
 const h=harness();let r=await h.run(0,null,{});r.state.filterSince=date(150);
 h.set({rows:[{...thread(100,'subscribed'),id:'older'}]});r=await h.run(180,r.state,{reasons:null});assert.deepEqual(r.state.pending,{});assert.equal(h.calls.length,2);
 h.set({rows:[{...thread(220,'subscribed'),id:'older'}]});r=await h.run(270,r.state,{reasons:null});assert.ok(Object.keys(r.state.pending).length);
});
