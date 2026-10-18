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
 const run=(now,state=null,config={})=>poll({now:start+now*1000,state,config,accounts:{github:'123'}},{fetch:fake});
 return {run,calls,set(value){if(value.rows)rows=value.rows;if(value.comments)comments=value.comments;if(value.code)code=value.code;if(value.headers)headers=value.headers;}};
}
test('baseline, overlapping cursor, incremental comments, coalescing, 304, restart and manual history',async()=>{
 const h=harness();let r=await h.run(0);assert.equal(r.status,'baseline');assert.deepEqual(r.events,[]);assert.equal(h.calls.length,1);assert.equal(r.delay_seconds,90);
 h.set({rows:[thread(80)],comments:[comment(-1),comment(80)]});r=await h.run(90,r.state);assert.equal(r.events.length,0);assert.match(h.calls[1].url,/since=/);assert.ok(h.calls[1].headers['If-Modified-Since']);
 h.set({rows:[thread(160)],comments:[comment(80),comment(160)]});r=await h.run(180,JSON.parse(JSON.stringify(r.state)));assert.equal(r.events.length,0);
 h.set({code:304});r=await h.run(270,r.state);assert.equal(r.events.length,1);assert.match(r.events[0].body,/comment-160/);assert.match(r.events[0].body,/comment-80/);assert.doesNotMatch(r.events[0].body,/comment--1/);
 const id=r.events[0].id;r=await h.run(360,r.state);assert.equal(r.events.length,0);
 h.set({code:200,comments:[comment(-1),comment(80),comment(160)]});r=await h.run(450,r.state,{existing:'manual-request'});assert.equal(r.events.length,0);h.set({code:304});r=await h.run(540,r.state,{existing:'manual-request'});assert.equal(r.events.length,0);r=await h.run(630,JSON.parse(JSON.stringify(r.state)),{existing:'manual-request'});assert.equal(r.events.length,1);assert.notEqual(r.events[0].id,id);assert.match(r.events[0].body,/comment--1/);
});
test('filters establish progress without model events; account switch establishes a fresh baseline',async()=>{
 const h=harness();let r=await h.run(0);h.set({rows:[thread(80)]});r=await h.run(90,r.state,{repositories:['other/repo']});assert.deepEqual(r.events,[]);assert.equal(h.calls.length,2);assert.match(h.calls[1].url,/\/repos\/other\/repo\/notifications/);
 r=await h.run(180,{...r.state,account:'other'});assert.equal(r.status,'baseline');assert.equal(r.events.length,0);
});
test('permission, expiration, rate limits and network failures preserve cursor and back off',async()=>{
 for(const [code,headers,status]of [[403,{},'permission'],[401,{},'authorization'],[429,{'retry-after':'600'},'rate_limited'],[403,{'x-ratelimit-remaining':'0','x-ratelimit-reset':String((start+900000)/1000)},'rate_limited']]){
 const h=harness();const initial=await h.run(0);h.set({code,headers});const r=await h.run(90,initial.state);assert.equal(r.status,status);assert.equal(r.state.since,initial.state.since);assert.equal(r.events.length,0);assert.ok(r.delay_seconds>=60);
 }
 const h=harness(),initial=await h.run(0);let r=await poll({now:start+90000,state:initial.state,config:{},accounts:{github:'123'}},{fetch:async()=>{throw Error('offline');}});assert.equal(r.status,'backoff');assert.equal(r.state.since,initial.state.since);
});
test('pagination advances only after complete fetch; unsafe links never receive credentials',async()=>{
 let calls=0;
 const fetcher=async(url)=>{calls++;if(url.searchParams.get('page')==='2')return Response.json([thread(-2)]);return Response.json([thread(-1)],{headers:{link:'<https://api.github.com/notifications?page=2>; rel="next"'}});};
 let r=await poll({now:start,state:null,config:{},accounts:{github:'123'}},{fetch:fetcher});assert.equal(r.status,'baseline');assert.equal(calls,2);assert.equal(r.state.modified,null);
 calls=0;r=await poll({now:start,state:null,config:{},accounts:{github:'123'}},{fetch:async()=>{calls++;return Response.json([thread(-1)],{headers:{link:'<https://evil.test/notifications?page=2>; rel="next"'}});}});assert.equal(r.status,'permission');assert.equal(calls,1);assert.equal(r.state.baseline,false);
});
test('later mention with no comment remains a reason update; duplicate snapshots produce no new event',async()=>{
 const h=harness();let r=await h.run(0);h.set({rows:[thread(80,'review_requested')]});r=await h.run(90,r.state);r=await h.run(180,r.state);assert.equal(r.events.length,1);assert.match(r.events[0].body,/review_requested/);r=await h.run(270,r.state);assert.equal(r.events.length,0);
});
test('PR reviews and review comments are expanded; same-second new comments survive the boundary',async()=>{
 let rows=[thread(0)],responses=0;
 rows[0].subject.latest_comment_url='https://api.github.com/repos/me/repo/issues/comments/1';
 const initial=await poll({now:start,state:null,config:{},accounts:{github:'123'}},{fetch:async()=>Response.json(rows)});
 rows=[thread(0)];rows[0].subject.latest_comment_url='https://api.github.com/repos/me/repo/issues/comments/2';
 let r=await poll({now:start+90000,state:initial.state,config:{},accounts:{github:'123'}},{fetch:async url=>url.pathname==='/notifications'?Response.json(rows):url.pathname.endsWith('/comments')?Response.json([{...comment(0),id:1,body:'old'},{...comment(0),id:2,body:'new boundary comment'}]):Response.json({})});
 r=await poll({now:start+180000,state:r.state,config:{},accounts:{github:'123'}},{fetch:async()=>new Response(null,{status:304})});assert.equal(r.events.length,1);assert.match(r.events[0].body,/new boundary comment/);assert.doesNotMatch(r.events[0].body,/"body":"old"/);
 const pr=thread(80,'review_requested');pr.subject.type='PullRequest';pr.subject.url='https://api.github.com/repos/me/repo/pulls/1';
 r=await poll({now:start+270000,state:initial.state,config:{},accounts:{github:'123'}},{fetch:async url=>{
 responses++;if(url.pathname==='/notifications')return Response.json([pr]);if(url.pathname.endsWith('/reviews'))return Response.json([{id:3,body:'review submitted',submitted_at:date(80)}]);if(url.pathname.endsWith('/comments'))return Response.json([comment(80)]);return Response.json({});
 }});assert.equal(responses,5);
 r=await poll({now:start+360000,state:r.state,config:{},accounts:{github:'123'}},{fetch:async()=>new Response(null,{status:304})});assert.equal(r.events.length,1);assert.match(r.events[0].body,/review submitted/);assert.match(r.events[0].body,/review_comment/);
});
test('bounded Unicode titles and large escaped comment bodies remain valid events',async()=>{
 const h=harness();let r=await h.run(0);const next=thread(80);next.subject.title='通知'.repeat(200);h.set({rows:[next],comments:Array.from({length:40},(_,i)=>({...comment(80+i),body:'\u0001'.repeat(3000)}))});r=await h.run(90,r.state);h.set({code:304});r=await h.run(180,r.state);assert.equal(r.events.length,1);assert.ok(Buffer.byteLength(r.events[0].title)<=512);assert.ok(Buffer.byteLength(r.events[0].body)<=24000);assert.match(r.events[0].body,/"truncated":true/);
});
test('poll and retry intervals longer than a host wake remain respected across process restarts',async()=>{
 for(const [status,headers]of [[200,{'x-poll-interval':'172800'}],[429,{'retry-after':'172800'}]]){
 let calls=0;const fake=async()=>{calls++;return Response.json([],{status,headers});};
 let r=await poll({now:start,state:null,config:{},accounts:{github:'123'}},{fetch:fake});assert.equal(r.delay_seconds,86400);
 r=await poll({now:start+86400000,state:JSON.parse(JSON.stringify(r.state)),config:{},accounts:{github:'123'}},{fetch:fake});assert.equal(calls,1);assert.equal(r.events.length,0);
 await poll({now:start+172800000,state:r.state,config:{},accounts:{github:'123'}},{fetch:fake});assert.equal(calls,2);
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
