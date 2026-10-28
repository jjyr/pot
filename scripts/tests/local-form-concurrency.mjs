import assert from 'node:assert/strict';
import {installDOM,Element,input,deferred,tick} from './helpers/form-dom.mjs';
import {createPluginNotifications} from '../../web/src/components/plugin-notifications.mjs';
import {createProviderAllowances} from '../../web/src/components/provider-allowances.mjs';
import {createIntegrations} from '../../web/src/components/integrations.mjs';
import {createExtensions} from '../../web/src/components/extensions.mjs';
import {createPages} from '../../web/src/components/pages.mjs';

installDOM();
const scope={space:'one',owner:'a'},locale=()=>language;let language='en';
const root=()=>{const node=new Element('main');document.body.append(node);return node;};
const button=(host,text)=>host.querySelectorAll('button').find(node=>node.textContent===text);
const field=(host,label)=>host.querySelectorAll('input,select,textarea').find(node=>node.getAttribute('aria-label')===label);
const filters=value=>({repositories:[value],reasons:null,subject_types:null,threads:null});
const sub=(id='a',revision=1,value='initial')=>({id,revision,enabled:true,assistant:'a',filters:filters(value)});
const account={identity:{id:'account',username:'fixture'},revision:1};
const projection=(revision,subs,operation)=>({items:[{id:'github',settings:{account_id:'account',revision,subscriptions:subs,fields:[{key:'repositories',label:'pluginNotifications.repositories'}]}}],assistants:[{id:'a',name:'A'}],...(operation?{operation:{subscription_id:operation}}:{})});
const evidence={scope,forms:[],integrations:{},extensions:{}};

// Actual callbacks: immutable A, new B, independent row writes, out-of-order
// command projections and a stale GET cannot regress revisions or erase B.
{
 const host=root(),abort=new AbortController(),calls=[];let model;
 const render=()=>host.replaceChildren(model.form());
 model=createPluginNotifications({request:(path,options)=>{const gate=deferred();calls.push({path,options,...gate});return gate.promise;},scope,signal:abort.signal,locale,render,pluginID:'github'});
 model.update(projection(1,[sub(),sub('b')]),account);render();
 const row=id=>host.querySelectorAll('.page-section').find(node=>node.dataset.viewKey==='subscription-'+id);
 input(field(row('a'),'Repositories values (comma separated)'),'A');
 const first=button(row('a'),'Save notification settings').click();
 input(field(row('a'),'Repositories values (comma separated)'),'B');
 assert.equal(field(row('a'),'Repositories values (comma separated)').disabled,false);
 assert.equal(button(row('b'),'Save notification settings').disabled,false);
 const second=button(row('b'),'Save notification settings').click();
 assert.deepEqual(calls[0].options.body.arguments.filters.repositories,['A']);
 assert.equal(calls.length,2);
 calls[1].resolve(projection(3,[sub('a',2,'A'),sub('b',2)],'b'));await second;
 calls[0].resolve(projection(2,[sub('a',2,'A'),sub('b')],'a'));await first;
 assert.match(row('b').textContent,/Revision: 2/);
 assert.equal(field(row('a'),'Repositories values (comma separated)').value,'B');
 model.update(projection(1,[sub(),sub('b')]),account);render();
 assert.match(row('b').textContent,/Revision: 2/,'stale GET must not undo a command');
 const next=button(row('a'),'Save notification settings').click();
 assert.equal(calls[2].options.body.arguments.revision,2);assert.deepEqual(calls[2].options.body.arguments.filters.repositories,['B']);
 calls[2].reject(Error('synthetic lost response'));await next;
 input(field(row('a'),'Repositories values (comma separated)'),'C');
 assert.match(row('a').textContent,/edits made since it started have not been submitted/);
 const retry=button(row('a'),'Retry previous save').click();
 assert.deepEqual(calls[3].options.body,calls[2].options.body,'unknown retry resends A with the same request ID, never C');
 calls[3].resolve(projection(4,[sub('a',3,'B'),sub('b',2)],'a'));await retry;
 assert.equal(field(row('a'),'Repositories values (comma separated)').value,'C');
 const final=button(row('a'),'Save notification settings').click();assert.equal(calls[4].options.body.arguments.revision,3);assert.deepEqual(calls[4].options.body.arguments.filters.repositories,['C']);assert.notEqual(calls[4].options.body.arguments.request_id,calls[3].options.body.arguments.request_id);
 calls[4].resolve(projection(5,[sub('a',4,'C'),sub('b',2)],'a'));await final;
 // A concurrent creation placed first in the complete catalogue is not ours.
 button(host,'Add subscription').click();input(field(row('new'),'Repositories'),'selected','change');const create=button(row('new'),'Save notification settings').click();input(field(row('new'),'Repositories values (comma separated)'),'new B');
 calls[5].resolve(projection(6,[sub('foreign'),sub('ours'),sub('a',4,'C'),sub('b',2)],'ours'));await create;
 assert.equal(field(row('ours'),'Repositories values (comma separated)').value,'new B');assert(!row('new'));
 // A successful deletion, with B typed during it, retains a fenced orphan draft.
 const remove=button(row('ours'),'Delete subscription').click();input(field(row('ours'),'Repositories values (comma separated)'),'delete B');calls[6].reject(Error('unknown deletion'));await remove;
 assert(button(row('ours'),'Retry previous deletion'));
 const retryDelete=button(row('ours'),'Retry previous deletion').click();assert.deepEqual(calls[7].options.body,calls[6].options.body);calls[7].resolve(projection(7,[sub('foreign'),sub('a',4,'C'),sub('b',2)],'ours'));await retryDelete;
 assert.equal(field(row('ours'),'Repositories values (comma separated)').value,'delete B');assert.match(row('ours').textContent,/no longer appears/);assert.equal(button(row('ours'),'Save notification settings').disabled,true);
 // External deletion after an unknown outcome must neither clear B nor turn
 // its retry into a creation of a different subscription.
 const external=button(row('foreign'),'Delete subscription').click();input(field(row('foreign'),'Repositories values (comma separated)'),'external B');calls[8].reject(Error('unknown external race'));await external;model.update(projection(8,[sub('a',4,'C'),sub('b',2)]),account);render();const externalRetry=button(row('foreign'),'Retry previous deletion').click();assert.deepEqual(calls[9].options.body,calls[8].options.body);calls[9].reject(Object.assign(Error('stale_subscription_revision'),{status:409}));await externalRetry;assert.equal(field(row('foreign'),'Repositories values (comma separated)').value,'external B');assert.equal(button(row('foreign'),'Save notification settings').disabled,true);
 evidence.forms.push({kind:'subscriptions',posts:calls.length,independentInflight:2,unknownRetries:3,receiptID:'ours',staleGETRejected:true,deletedDraftRetained:true,externalDeletionFenced:true});abort.abort();host.remove();
}

// No receipt: preserve B and expose a deliberate local reset, without another POST.
{
 const host=root(),abort=new AbortController(),gate=deferred();let model,posts=0;
 const render=()=>host.replaceChildren(model.form());model=createPluginNotifications({request:()=>{posts++;return gate.promise;},scope,signal:abort.signal,locale,render,pluginID:'github'});model.update(projection(1,[]),account);render();button(host,'Add subscription').click();input(field(host,'Repositories'),'selected','change');const save=button(host,'Save notification settings').click();input(field(host,'Repositories values (comma separated)'),'B');gate.resolve(projection(2,[sub('unconfirmed')]));await save;assert.match(host.textContent,/server accepted the operation/);assert.equal(posts,1);assert.equal(button(host,'Awaiting confirmation').disabled,true);assert(button(host,'Discard draft'));abort.abort();host.remove();
 evidence.forms.push({kind:'missing-receipt',posts,blockedDuplicate:true});
}

// Allowance acknowledgement advances only the submitted baseline. Locale and
// a failed second write preserve the currently editable reminder draft.
{
 const host=root(),memory={},writes=[],value={monitor:{enabled:true,threshold:80,assistant:'a',revision:1},assistants:[{id:'a',name:'A'}],status:'ready'};
 const domain={allowance:async()=>value,configureAllowance:(_,id,values,revision)=>{const gate=deferred();writes.push({values,revision,...gate});return gate.promise;}};
 const update=createProviderAllowances({root:host,providersDomain:domain,scope,profileID:'p',locale,memory});host.querySelector('details').open=true;host.querySelector('details').dispatchEvent(new Event('toggle'));await tick();
 input(field(host,'Alert at used percentage'),'81');const save=button(host,'Save reminder').click();input(field(host,'Alert at used percentage'),'82');assert.equal(field(host,'Alert at used percentage').disabled,false);assert.equal(button(host,'Save reminder').disabled,true);
 writes[0].resolve({...value,monitor:{...value.monitor,threshold:81,revision:2}});await save;assert.equal(memory.draft.threshold,82);assert.equal(memory.baseline.threshold,81);assert.equal(memory.revision,2);
 const failed=button(host,'Save reminder').click();input(field(host,'Alert at used percentage'),'83');writes[1].reject(Error('synthetic failure'));await failed;assert.equal(memory.draft.threshold,83);assert.equal(memory.baseline.threshold,81);
 language='zh-CN';update();assert.equal(field(host,'已用百分比达到此值时提醒').value,83);language='en';update();update.dispose();host.remove();evidence.forms.push({kind:'allowance',writes:writes.length,newDraft:83,acknowledged:81});
}

// Integrations render real rows while notification IO is unresolved. A login
// operation does not lock another account or the pure GitHub disclosure.
{
 const host=root(),abort=new AbortController(),calls=[],listeners=new Set();
 const view=createIntegrations({root:host,request:(path,options)=>{const gate=deferred();calls.push({path,options,...gate});return gate.promise;},scope,signal:abort.signal,locale,openExternal:async()=>{},resources:{subscribeInvalidation:callback=>{listeners.add(callback);return()=>listeners.delete(callback);}}});
 const refresh=view.refresh();calls[0].resolve({items:[{id:'github',name:'GitHub',account,status:'connected'},{id:'other',name:'Other',login_available:true},{id:'third',name:'Third',login_available:true}]});await tick();assert(host.querySelector('.integration-toggle'),'accounts render before slow notification GET');host.querySelector('.integration-toggle').click();assert.equal(host.querySelector('.integration-toggle').getAttribute('aria-expanded'),'true');
 calls[1].resolve(projection(1,[sub()]));await refresh;await tick();
 const rows=()=>host.querySelectorAll('.integration-row'),other=()=>rows().find(row=>row.dataset.viewKey==='other');
 const login=button(other(),'Log in').click();assert.equal(calls[2].path,'/api/v1/integrations/other/login');assert.equal(host.querySelector('.integration-toggle').disabled,false);assert.equal(button(rows().find(row=>row.dataset.viewKey==='third'),'Log in').disabled,false);calls[2].resolve({authorization_url:'https://fixture.invalid/auth'});await login;
 // An old notification GET resolves after a newer command response.
 for(const listener of listeners)listener(['plugin-notifications'],scope);await tick();const old=calls.at(-1);const save=button(host,'Save notification settings').click();const mutation=calls.at(-1);mutation.resolve(projection(3,[sub('a',2,'saved')],'a'));await save;old.resolve(projection(1,[sub()]));await tick();
 for(const listener of listeners)listener(['integrations'],scope);await tick();calls.at(-1).resolve({items:[{id:'github',name:'GitHub',account,status:'connected'}]});await tick();assert.match(host.textContent,/Revision: 2/,'account refresh cannot rehydrate older notificationValue');
 evidence.integrations={accountGETs:calls.filter(call=>call.path==='/api/v1/integrations').length,notificationGETs:calls.filter(call=>call.path==='/api/v1/plugin-notifications').length,independentRows:true,staleCommandFence:true};abort.abort();host.remove();
}

// MCP editors retain B and use the exact returned write revision for its next
// save. Local search and filters stay available while A is pending.
{
 const host=root(),calls=[],catalog={items:[],mcp:[{kind:'mcp',id:'m',name:'M',enabled:true,revision:1,transport:'http',url:'https://fixture.invalid/mcp'}]};
 const component=createExtensions({root:host,locale,client:{runtime:{id:'fixture',boot_id:'b'},scope:()=>scope,request:async(path,options={})=>{if(options.method==='POST'){const gate=deferred();calls.push({path,options,...gate});return gate.promise;}return catalog;}}});await component.load();host.querySelector('.extension-open').click();button(document.body.querySelector('.extensions-dialog'),'Edit configuration').click();const pane=()=>document.body.querySelector('.extensions-dialog');
 input(field(pane(),'Name'),'A');const save=button(pane(),'Save').click();input(field(pane(),'Name'),'B');assert.equal(host.querySelector('.extension-search').disabled,false);assert.equal(field(pane(),'Name').disabled,false);assert.equal(button(pane(),'Save').disabled,true);assert.equal(calls[0].options.body.server.name,'A');catalog.mcp[0]={...catalog.mcp[0],name:'A',revision:2};calls[0].resolve(catalog.mcp[0]);await save;assert(pane());assert.equal(field(pane(),'Name').value,'B');const next=button(pane(),'Save').click();assert.equal(calls[1].options.body.server.revision,2);assert.equal(calls[1].options.body.server.name,'B');calls[1].reject(Error('synthetic failure'));await next;assert.equal(field(pane(),'Name').value,'B');component.dispose();host.remove();evidence.extensions={writes:calls.length,newDraft:'B',acknowledgedRevision:2};
}
// A created skill becomes the exact returned identity before acknowledging B;
// a later save updates that skill with its verified digest rather than creating
// another directory from the still-mounted creation form.
{
 const host=root(),calls=[],catalog={items:[],mcp:[]};let savedContent='';
 const component=createExtensions({root:host,locale,client:{runtime:{id:'fixture',boot_id:'boot'},scope:()=>scope,request:async(path,options={})=>{
  if(options.method==='POST'){const gate=deferred();calls.push({path,options,...gate});return gate.promise;}
  if(path.includes('/skills/'))return {content:savedContent,digest:'verified-digest'};
  return catalog;
 }}});await component.load();const extensionButton=(container,key)=>container.querySelectorAll('button').find(node=>node.dataset.extensionText===key);
 host.querySelectorAll('button').find(node=>node.getAttribute('aria-label')==='Add Skill').click();extensionButton(document.body.querySelector('.extensions-dialog'),'createSkill').click();const pane=()=>document.body.querySelector('.extensions-dialog');
 input(field(pane(),'Name'),'fixture-skill');input(field(pane(),'Content'),'submitted A');const save=extensionButton(pane(),'save').click();input(field(pane(),'Content'),'new B');savedContent='submitted A';catalog.items=[{id:'skill-id',kind:'skills',name:'fixture-skill',source:'user',revision:1,enabled:true}];calls[0].resolve({id:'skill-id',saved:true});await save;assert.equal(field(pane(),'Content').value,'new B');const next=extensionButton(pane(),'save').click();assert.equal(calls[1].options.body.action,'save');assert.equal(calls[1].options.body.id,'skill-id');assert.equal(calls[1].options.body.revision,1);assert.equal(calls[1].options.body.digest,'verified-digest');assert.equal(calls[1].options.body.content,'new B');calls[1].reject(Error('synthetic stale skill'));await next;assert.equal(field(pane(),'Content').value,'new B');component.dispose();host.remove();evidence.forms.push({kind:'skill',writes:calls.length,identity:'skill-id',verifiedDigest:true});
}

// Actual assistant editor: slow avatar processing disables duplicate avatar
// actions and submit while an unrelated name remains editable.
{
 const host=root(),approvalHost=root(),chatApprovalHost=root(),avatar=deferred(),write=deferred(),posts=[];
 const definition={id:'a',space_id:'one',name:'Initial',revision:1,capabilities:{version:1,operations:['assistant.configure']}};
 const client={runtime:{id:'fixture',boot_id:'boot'},spaces:[{id:'one',control_id:'fixture'}],assistants:[definition],scope:()=>scope,request:async(path,options={})=>{
  if(path==='/api/v1/assistants/default-avatar')return avatar.promise;
  if(options.method==='POST'){posts.push({path,options});return write.promise;}
  if(path==='/api/v1/providers')return {profiles:[]};
  if(path==='/api/v1/instructions')return {text:'',revision:'rules'};
  return {generation:1,revision:1};
 }};
 const pages=createPages({client,host,approvalHost,chatApprovalHost,chatVisible:()=>false,locale,notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});await pages.assistant(definition);await tick();
 const reset=button(host,'Restore default avatar');assert(reset,'assistant editor exposes default avatar action: '+host.textContent);
 const pending=reset.click();input(field(host,'Name'),'B');assert.equal(field(host,'Name').disabled,false);assert.equal(button(host,'Save').disabled,true);
 avatar.resolve({avatar:'data:image/png;base64,c3ludGhldGlj'});await pending;assert.equal(field(host,'Name').value,'B');const form=host.querySelector('form'),save=form.onsubmit({preventDefault(){}});await tick();assert.equal(posts[0].options.body.name,'B');input(field(host,'Name'),'C');write.resolve({revision:2});await save;assert.equal(field(host,'Name').value,'C');assert.match(host.textContent,/saved|Saved/);pages.dispose();host.remove();approvalHost.remove();chatApprovalHost.remove();evidence.forms.push({kind:'avatar',nameDuringProcessing:'B',nameDuringSave:'C',posts:posts.length});
}
console.log(JSON.stringify(evidence,null,2));
