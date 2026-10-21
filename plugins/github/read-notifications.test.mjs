import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readNotifications} from './read-notifications.mjs';
import {createPlugin} from './index.mjs';
process.env.GH_TOKEN='fixture-token';
const row={id:'12345',reason:'subscribed',updated_at:'2026-01-01T00:00:00Z',unread:false,repository:{full_name:'nervosnetwork/fiber'},subject:{type:'PullRequest',title:'PR 1701',url:'https://api.github.com/repos/nervosnetwork/fiber/pulls/1701'}};
test('active reads are paginated GETs, independent of realtime state and mark-read',async()=>{
 const calls=[];const fetch=async(url,init)=>{calls.push([String(url),init]);if(new URL(url).searchParams.get('page')==='2')return Response.json([]);return Response.json([row],{headers:{link:'<https://api.github.com/notifications?page=2>; rel="next"'}});};
 const result=await readNotifications({all:true,participating:true,since:'2025-12-31T00:00:00Z',before:'2026-01-02T00:00:00Z',per_page:50},{fetch});
 assert.equal(result.items.length,1);assert.equal(result.items[0].thread_id,'12345');assert.equal(result.items[0].url,'https://github.com/nervosnetwork/fiber/pull/1701');assert.equal(result.next_page,2);assert.equal(calls.length,1);assert.equal(calls[0][1].method,'GET');
 const url=new URL(calls[0][0]);for(const [key,val]of Object.entries({all:'true',participating:'true',per_page:'50',page:'1'}))assert.equal(url.searchParams.get(key),val);
 const none=await readNotifications({reasons:['mention']},{fetch});assert.deepEqual(none.items,[]);assert.equal(none.next_page,2);
 const last=await readNotifications({page:result.next_page,all:true,since:'2025-12-31T00:00:00Z'},{fetch});assert.deepEqual(last.items,[]);assert.equal(last.next_page,null);assert.equal(new URL(calls.at(-1)[0]).searchParams.get('page'),'2');
 const duplicate=await readNotifications({},{fetch});assert.equal(duplicate.items[0].event_id,result.items[0].event_id);assert.equal('state' in result,false);
});
test('actual plugin tool routes read without gh and preserves permission failures',async()=>{
 const original=globalThis.fetch,events=[];
 try{
 globalThis.fetch=async()=>Response.json([row]);const plugin=createPlugin({emit:event=>events.push(event)});
 await plugin.execute({tool:'github_read_notifications',input:{threads:['12345']}});assert.equal(JSON.parse(events[0].text).items[0].thread_id,'12345');
 globalThis.fetch=async()=>new Response(null,{status:403});await assert.rejects(plugin.execute({tool:'github_read_notifications',input:{}}),/github_notifications_permission/);
 }finally{globalThis.fetch=original;}
});
test('query validation, permission failures and unsafe pagination stop before extra requests',async()=>{
 for(const input of [{page:0},{per_page:101},{all:'true'},{since:'yesterday'},{reason:'direct_reply'}])await assert.rejects(readNotifications(input,{fetch:async()=>{throw Error('must not fetch');}}));
 await assert.rejects(readNotifications({},{fetch:async()=>Response.json([row],{headers:{link:'<https://evil.test/notifications?page=2>; rel="next"'}})}),/invalid_github_pagination/);
 await assert.rejects(readNotifications({},{fetch:async()=>Response.json([null])}),/invalid_github_notification_response/);
});
