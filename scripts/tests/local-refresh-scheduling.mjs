import assert from 'node:assert/strict';
import {installDOM,Element,deferred,tick} from './helpers/form-dom.mjs';
import {createExtensions} from '../../web/src/components/extensions.mjs';
import {domainsFor} from '../../web/src/state/domains/index.mjs';
import {createQueryRegistry,createCommands} from '../../web/src/state/domains/query.mjs';
import {createResourceReads} from '../../web/src/state/resource-cache.mjs';
import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import {createProvidersDomain} from '../../web/src/state/domains/providers.mjs';
import {createPages} from '../../web/src/components/pages.mjs';
import {createProductionClient,mutationTopics} from '../../web/src/adapters/production.mjs';

installDOM();
const original={now:Date.now,setTimeout,clearTimeout,setInterval,clearInterval};
let now=100000,serial=0;const timers=new Map();
Date.now=()=>now;globalThis.setTimeout=(callback,delay=0)=>{const id=++serial;timers.set(id,{callback,at:now+delay});return id;};globalThis.clearTimeout=id=>timers.delete(id);
globalThis.setInterval=(callback,delay)=>{const id=++serial;timers.set(id,{callback,at:now+delay,interval:delay});return id;};globalThis.clearInterval=clearTimeout;
async function advance(ms){const end=now+ms;let count=0;for(;;){const next=[...timers].filter(([,timer])=>timer.at<=end).sort((a,b)=>a[1].at-b[1].at)[0];if(!next)break;assert(++count<1000,'bounded clock callbacks');const [id,timer]=next;now=timer.at;if(timer.interval)timer.at+=timer.interval;else timers.delete(id);timer.callback();await tick();}now=end;await tick();}
const scope={space:'one',owner:'a'},evidence={oauth:{},providers:{},permissions:{}};
const host=()=>{const node=new Element('main');document.body.append(node);return node;};
const button=(root,key)=>root.querySelectorAll('button').find(node=>node.dataset.extensionText===key);
try{
 assert.equal(mutationTopics('/api/v1/extensions/mcp/oauth/begin'),null,'authorization handoff changes no catalogue projection');
 assert(mutationTopics('/api/v1/extensions/mcp/oauth/complete').includes('extensions'),'accepted OAuth credentials still invalidate the catalogue');
 // Actual browser transport ownership (a synthetic EventSource, no browser or
 // network): opening is insufficient, boot/generation and idle retirement fence
 // health. Native has no equivalent proof and therefore retains fallback.
 {
  const previousFetch=globalThis.fetch,previousSource=globalThis.EventSource,sources=[];
  globalThis.fetch=async()=>new Response(JSON.stringify({runtime_id:'fixture',boot_id:'boot'}));
  globalThis.EventSource=class extends EventTarget{static CLOSED=2;constructor(){super();this.readyState=1;sources.push(this);}close(){this.readyState=2;}emit(boot){const event=new Event('changed');Object.assign(event,{data:JSON.stringify({topics:{permissions:1}}),lastEventId:boot+':1'});this.dispatchEvent(event);}};
  const client=await createProductionClient({resourceStorage:createMemoryProjectionStore()}),stop=client.changes(()=>{},{space:'one'});
  assert.equal(client.changesHealthy('one'),false);sources[0].emit('boot');assert.equal(client.changesHealthy('one'),true);assert.equal(client.changesHealthy('two'),false);
  client.connectionGeneration++;assert.equal(client.changesHealthy('one'),false);client.connectionGeneration--;client.runtime.boot_id='other';assert.equal(client.changesHealthy('one'),false);client.runtime.boot_id='boot';
  await advance(45000);assert.equal(client.changesHealthy('one'),false,'idle retirement cannot leave a remembered healthy flag');stop();client.resources.clear();globalThis.fetch=previousFetch;globalThis.EventSource=previousSource;
  evidence.permissions.transportHealth={acceptedFrameRequired:true,scopeBootGenerationFence:true,idleRetired:true};
 }
 // Two consumers share one slow catalog read. OAuth fallback never overlaps
 // itself; a changed revision adopts that exact response without a second GET.
 {
  const root=host(),calls=[],popup={location:'',close(){}};window.open=()=>popup;let inflight=0,maxInflight=0,publishes=0;
  const client={runtime:{id:'fixture',boot_id:'boot'},connectionGeneration:1,scope:()=>scope,request:(path,options={})=>{
   if(options.method==='POST'){assert.equal(options.transient,true,'OAuth session response must bypass persistent command receipts');return Promise.resolve({state:'synthetic',authorization_url:'https://fixture.invalid/auth'});}
   const gate=deferred();inflight++;maxInflight=Math.max(maxInflight,inflight);calls.push({scope:{space:options.space,owner:options.owner},path,...gate});return gate.promise.finally(()=>inflight--);
  }};
  const component=createExtensions({root,client,locale:()=> 'en'}),domain=domainsFor(client).extensions;
  const initial=component.load(),shared=domain.catalog(scope).value();await tick();assert.equal(calls.length,1);const catalog=revision=>({items:[],mcp:[{id:'m',name:'M',revision,auth:'oauth',transport:'http',url:'https://fixture.invalid/mcp',enabled:true}],authorization_url:'must-not-survive',oauth_session:{secret:'synthetic'}});
  calls[0].resolve(catalog(1));await Promise.all([initial,shared]);assert.equal(domain.catalog(scope).snapshot.data.oauth_session,undefined);assert.equal(domain.catalog(scope).snapshot.data.authorization_url,undefined);
  root.querySelector('.extension-open').click();const connect=button(document.body.querySelector('.extensions-dialog'),'oauthConnect').click();await tick();assert.equal(calls.length,2,'OAuth handoff confirms the live target first');calls[1].resolve(catalog(1));await connect;
  const list=root.querySelector('.extensions-list'),replace=list.replaceChildren.bind(list);list.replaceChildren=(...nodes)=>{publishes++;return replace(...nodes);};
  await advance(1500);assert.equal(calls.length,3);const completion=new Event('message');Object.assign(completion,{origin:location.origin,source:popup,data:{type:'pot:mcp-oauth',state:'synthetic',completed:true}});window.dispatchEvent(completion);await tick();await advance(30000);assert.equal(calls.length,3,'slow 30s read must remain single flight');calls[2].resolve(catalog(2));await tick();assert.equal(calls.length,3,'reuse the complete changed catalog');assert.equal(publishes,1);await advance(60000);assert.equal(calls.length,3,'successful OAuth cancels its fallback');
  // Dispose an OAuth generation with a GET in flight. No late UI publication.
  root.querySelector('.extension-open').click();const reconnect=button(document.body.querySelector('.extensions-dialog'),'oauthConnect').click();await tick();assert.equal(calls.length,4);calls[3].resolve(catalog(2));await reconnect;await advance(1500);assert.equal(calls.length,5);component.dispose();const before=publishes;calls[4].resolve(catalog(3));await tick();await advance(60000);assert.equal(publishes,before);assert.equal(inflight,0);
  evidence.oauth={GETs:calls.length,maxInflight,publishes,scopes:calls.map(call=>call.scope),latePublish:0};root.remove();
 }
 // Different owner scopes cannot join or adopt each other's catalog read.
 {
  const calls=[],client={runtime:{id:'fixture',boot_id:'boot'},scope:()=>scope,request:(path,options)=>{const gate=deferred();calls.push({options,...gate});return gate.promise;}};
  const domains=domainsFor(client),release=domains.retain(),one=domains.extensions.catalog(scope),two=domains.extensions.catalog({...scope,owner:'b'}),a=one.value(),b=two.value();await tick();assert.equal(calls.length,2);calls[1].resolve({items:[],mcp:[{id:'b'}]});await b;calls[0].resolve({items:[],mcp:[{id:'a'}]});await a;assert.equal(one.snapshot.data.mcp[0].id,'a');assert.equal(two.snapshot.data.mcp[0].id,'b');release();evidence.oauth.independentScopes=2;
 }
 // An event burst during an extension read fences that response once. The
 // query's later local reassembly reuses the read that crossed the same fence.
 {
  const calls=[],client={runtime:{id:'fixture',boot_id:'boot'},scope:()=>scope,request:()=>{const gate=deferred();calls.push(gate);return gate.promise;}};
  const domains=domainsFor(client),release=domains.retain(),query=domains.extensions.catalog(scope),stop=query.watch(()=>{});await tick();calls[0].resolve({items:[],mcp:[]});await tick();const events=[domains.changed(['extensions'],scope)];await tick();for(let i=0;i<20;i++)events.push(domains.changed(['extensions'],scope));assert.equal(calls.length,2);calls[1].resolve({items:[{id:'old'}],mcp:[]});await tick();assert.equal(calls.length,3);calls[2].resolve({items:[{id:'current'}],mcp:[]});await Promise.all(events);await tick();assert.equal(calls.length,3,'one follow-up, no fourth GET for query reassembly');assert.equal(query.snapshot.data.items[0].id,'current');stop();release();evidence.oauth.eventBurstGETs=3;
 }
 // Actual command receipt + epoch invalidation + query stack: setDefault joins
 // event revalidation; its completion no longer upgrades it to forced IO.
 {
  const storage=createMemoryProjectionStore(),calls=[];let commits=0,receipts=0,publications=0;
  const commit=storage.commit.bind(storage),command=storage.command.bind(storage);storage.commit=async(...args)=>{commits++;return commit(...args);};storage.command=async(...args)=>{receipts++;return command(...args);};
  const client={runtime:{id:'fixture',boot_id:'boot'},scope:()=>scope,request:async(path,options={})=>{if(options.method==='POST'){await client.resources.applyCommand(path,options,{saved:true},['providers']);return {saved:true};}const gate=deferred();calls.push({path,options,...gate});return gate.promise;}};
  const resources=client.resources=createResourceReads(client,{storage}),queries=createQueryRegistry(resources),commands=createCommands(client),domain=createProvidersDomain(client,{resources,queries,commands});const stop=domain.watchCatalog(scope,state=>{if(state.data)publications++;});await tick();calls[0].resolve({profiles:[],default_provider_id:'old'});await tick();
  const set=domain.setDefault(scope,'new');await tick();assert.equal(calls.length,2);calls[1].resolve({profiles:[],default_provider_id:'new'});await set;await tick();assert.equal(calls.length,2,'command completion uses the invalidated resource, not a second forced GET');
  const unrelated=resources.read('/api/v1/usage/providers',{...scope,owner:''});await tick();calls[2].resolve({items:[]});await unrelated;await tick();const before=publications;
  const background=resources.read('/api/v1/usage/providers',{...scope,owner:'',authoritative:true});await tick();calls[3].resolve({items:[{provider:'synthetic'}]});await background;await tick();assert.equal(publications,before,'concrete paths reject unrelated same-topic commits');
  const forced=domain.refresh(scope);await tick();assert.equal(calls.length,5,'explicit Retry must still issue a real GET');calls[4].reject(Error('explicit refresh failed'));await assert.rejects(forced,/explicit refresh failed/);await tick();
  stop();queries.dispose();commands.dispose();resources.clear();evidence.providers={GETs:calls.length,providerGETs:calls.filter(call=>call.path==='/api/v1/providers').length,setDefaultGETs:2,explicitFailedGETs:1,commits,receipts,publications,unrelatedPublication:0};
 }
 // Actual page permissions callback: healthy local changes suppress routine
 // GETs, linked owners recover, and unhealthy native/stream status keeps bounds.
 {
  const root=host(),approvalHost=host(),chatApprovalHost=host();let healthy=true,linked=false,reads=0;
  const assistant={id:'a',space_id:'one',capabilities:{version:1,operations:['approvals.manage']}};
  const client={runtime:{id:'fixture',boot_id:'boot'},spaces:[{id:'one',control_id:'fixture'}],get assistants(){return [{...assistant,...(linked?{origin:{link_id:'synthetic'}}:{})}];},scope:()=>scope,changesHealthy:()=>healthy,request:async path=>{if(path==='/api/v1/permissions'){reads++;return {pending:[]};}return {runtime_id:'fixture'};}};
  const pages=createPages({client,host:root,approvalHost,chatApprovalHost,chatVisible:()=>true,locale:()=> 'en',notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});await pages.permissions();assert.equal(reads,1);await advance(30000);assert.equal(reads,1);healthy=false;await advance(5000);assert.equal(reads,2);linked=true;healthy=true;await advance(5000);assert.equal(reads,3);pages.dispose();root.remove();approvalHost.remove();chatApprovalHost.remove();Object.assign(evidence.permissions,{initialGET:1,healthyLocalGETsIn30s:0,recoveryGETs:2});
 }
 // Topology expiry redraws perform no IO. Its slower observation recovery
 // reuses resource TTLs and never rereads permission authority on a clock tick.
 {
  const root=host(),approvalHost=host(),chatApprovalHost=host(),calls=[];
  const client={runtime:{id:'fixture',boot_id:'boot'},spaces:[{id:'one',name:'One',control_id:'fixture',kind:'local'}],assistants:[],scope:()=>scope,request:async path=>{calls.push(path);if(path==='/api/v1/permissions')return {policy:{machines:{fixture:{}}}};if(path==='/api/v1/runtime')return {runtime_id:'fixture',host:{hostname:'fixture'}};if(path==='/api/v1/agent-links')return {items:[],self:{node_id:'fixture',observation:{observed_at_ms:now,value:{assistants:[]}}}};throw Error('Unexpected fixture path '+path);}};
  const pages=createPages({client,host:root,approvalHost,chatApprovalHost,chatVisible:()=>false,locale:()=> 'en',notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});await pages.show('machines');assert.equal(calls.filter(path=>path==='/api/v1/permissions').length,1);assert.equal(calls.filter(path=>path==='/api/v1/agent-links').length,1,'reuse the complete graph read for connection controls');const initial=calls.length;
  root.querySelector('.topology-edge-hit').click();const inspector=root.querySelector('.topology-inspector'),edgeText=inspector.textContent,edgeAction=inspector.querySelector('button');edgeAction.focus();const focusedAction=edgeAction.textContent;
  await advance(20000);assert.equal(calls.length,initial,'expiry presentation has no GET');assert.equal(inspector.textContent,edgeText,'clock expiry must retain the selected edge inspector');assert.equal(document.activeElement.textContent,focusedAction,'local expiry retains the focused inspector action');await advance(10000);await tick();assert.equal(calls.filter(path=>path==='/api/v1/permissions').length,1,'observation recovery must not poll authoritative policy');assert.equal(calls.filter(path=>path==='/api/v1/agent-links').length,2);assert.equal(calls.filter(path=>path==='/api/v1/runtime').length,2);
  await pages.refresh(['permissions'],{space:'one'});await tick();assert.equal(calls.filter(path=>path==='/api/v1/permissions').length,2,'permission event still reads current authority');pages.dispose();root.remove();approvalHost.remove();chatApprovalHost.remove();evidence.topology={expiryGETs:0,observationGETs:2,initialPermissionGETs:1,eventPermissionGETs:1};
 }
 console.log(JSON.stringify(evidence,null,2));
}finally{Date.now=original.now;Object.assign(globalThis,{setTimeout:original.setTimeout,clearTimeout:original.clearTimeout,setInterval:original.setInterval,clearInterval:original.clearInterval});}
