import { installWSFixture } from './helpers/ws-fixture.mjs';
const wsFixture=installWSFixture();
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import assert from 'node:assert/strict';
import {projectLinkedAssistants,linkedAssistantID,supportsCapability,agentFailureKey} from '../../web/src/state/agent-links.mjs';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
const capabilities={version:1,operations:['chat.read','chat.send','chat.receipt','chat.subscribe','task.submit','task.read','task.list','task.cancel','task.archive','task.outcomes']};
const space={id:'local',control_id:'local-node'},directory=[{address:{node_id:'peer-node',kind:'assistant',agent_id:'review:one'},name:'Reviewer',capabilities,adapter:'codex',reply_id:'reply-one',reply_count:2,last_reply_at_ms:3}];
const links={items:[{id:'first',peer:{node_id:'peer-node',kind:'codex',name:'Codex'},status:'connected',assistants:directory},{id:'second',peer:{node_id:'peer-two',kind:'codex',name:'Codex'},status:'disconnected',directory:{targets:directory}}]};
const projected=projectLinkedAssistants(links,space);
assert.equal(projected.length,2);
assert.equal(projected[0].reply_id,'reply-one');assert.equal(projected[0].reply_count,2);assert.equal(projected[0].last_reply_at_ms,3);
assert.notEqual(projected[0].id,projected[1].id,'Identical assistant IDs on different connections stay distinct');
assert.equal(projected[1].offline,true,'Disconnect keeps assistants visible with their offline state');
assert.equal(projectLinkedAssistants({items:[]},space).length,0,'Deleting a connection removes its directory entries');
const calls=[],frame={kind:'reset',view:{replies:[],state:{cursor:1,boot_id:'boot',conversation_id:'mail-conversation'},history:{has_more:false,has_newer:false},messages:[]}};
globalThis.fetch=async(url,options)=>{
 calls.push({url,options});
 if(url.includes('/api/v1/turn/cancel'))return new Response(JSON.stringify({code:'agent_link_capability_unsupported'}),{status:422});
 const value=url==='/api/v1/runtime'?{runtime_id:'local-node',boot_id:'boot'}:options.method==='POST'?{message_id:'accepted-message',duplicate:false}:frame;
 return new Response(JSON.stringify(value));
};
const client=await createProductionClient({resourceStorage:createMemoryProjectionStore()});client.assistants=projected;client.setScope({space:'local',owner:projected[0].id});
client.uiLocale=()=> 'zh-CN';
assert.equal((await client.snapshot()).owner_id,JSON.stringify(['local',projected[0].id]));
let route=new URL(calls.at(-1).url,'http://fixture');
assert.equal(route.pathname,'/api/v1/snapshot');assert.equal(route.searchParams.get('assistant_id'),projected[0].id,'The unified host port receives the exact assistant presentation key');
assert.equal(route.searchParams.get('locale'),'zh-CN','Initial linked read receives the current UI locale');
assert.equal(route.searchParams.get('link_id'),null,'The client does not choose a transport or an alternate business API');
await client.send({request_id:'same-request',conversation_id:'mail-conversation',text:'Review this'});
assert.deepEqual(JSON.parse(calls.at(-1).options.body),{request_id:'same-request',conversation_id:'mail-conversation',text:'Review this'});
await client.sendReceipt({request_id:'same-request',conversation_id:'mail-conversation'});
route=new URL(calls.at(-1).url,'http://fixture');assert.equal(route.searchParams.get('request_id'),'same-request');assert.equal(route.searchParams.get('conversation_id'),'mail-conversation');
await assert.rejects(client.cancel({request_id:'stop',turn_id:'turn'}),error=>error.code==='agent_link_capability_unsupported','Unsupported controls never fall back to local execution');
for(const owner of ['main',projected[0].id]){
 client.setScope({space:'local',owner});
 let delivered;const stop=client.subscribe(value=>{delivered=value;});await wsFixture.flush();const source=wsFixture.sockets.at(-1);const subscription=source.sent.findLast(item=>item.type==='subscribe');
 const streamRoute=new URL(subscription.path,'http://fixture');assert.equal(streamRoute.pathname,'/api/v1/events');assert.equal(streamRoute.searchParams.get('locale'),'zh-CN','Unified subscriptions carry the current locale');assert.equal(streamRoute.searchParams.get('assistant_id'),owner);
 const update={...frame,view:{...frame.view,replies:[{input_id:'input',state:'running'}]}};
 source.emit({type:'event',id:subscription.id,event:'conversation',data:update,event_id:'boot:1'});
 assert.equal(delivered.owner_id,JSON.stringify(['local',owner]),'Every endpoint retains its exact subscription owner');
 assert.deepEqual(delivered.replies,update.view.replies,'The same stream carries execution lifecycle for every owner');
 stop();assert.equal(source.subscriptions.has(subscription.id),false);
}

assert.equal(supportsCapability(projected[0],'task.list'),true);
assert.equal(supportsCapability({...projected[0],origin:null},'chat.cancel'),false,'Local-looking metadata never grants a capability');
assert.equal(supportsCapability({...projected[0],capabilities:{version:1,operations:['chat.cancel']}},'chat.cancel'),true,'Remote-looking metadata never blocks an advertised capability');
console.log('Agent Link directory identity, lifecycle projection, chat routing and shared subscriptions passed');

assert.equal(agentFailureKey('codex_host_restarted_outcome_unknown'),'agentLinks.codexOutcomeUnknown');
assert.equal(agentFailureKey('prefix: original_execution_unavailable'),'agentLinks.originalExecutionUnavailable');
assert.equal(agentFailureKey('arbitrary provider diagnostic'),null);
assert.equal(agentFailureKey('codex_thread_in_use: codex_rpc_rejected_thread_resume (-32600): thread busy-thread already has an active writer'),'agentLinks.codexSessionInUse');
assert.equal(agentFailureKey('codex_thread_open_failed: codex_rpc_rejected_thread_resume (-32600): session unavailable'),'agentLinks.codexSessionFailed','Other resume failures do not claim another client owns the conversation');

assert.equal(projectLinkedAssistants({items:[{id:'executor',peer:{node_id:'codex',kind:'codex'},status:'connected',directory:{targets:[{address:{kind:'assistant',agent_id:'worker'},name:'Worker',capabilities:{version:1,scope:'peer',operations:['assistant.profile','assistant.delete']}},{address:{kind:'node',node_id:'codex',agent_id:'codex'},capabilities:{version:1,scope:'caller',operations:['task.submit']}}]}}]},space).length,0,'A message-only assistant is not a direct user chat');
