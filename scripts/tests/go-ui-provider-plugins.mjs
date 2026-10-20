import assert from 'node:assert/strict';
import {createProviderSave,saveAllowanceConfiguration} from '../../web/src/state/provider-save.mjs';
import {createProviderAllowances,allowanceFreshness} from '../../web/src/components/provider-allowances.mjs';
import {providerConversationModels} from '../../web/src/state/models.mjs';
import {translate} from '../../web/src/locales/messages.mjs';

const calls=[];let failMonitor=true;
const persist=createProviderSave({save:async(values,context,profile)=>{calls.push({kind:'connection',values,context,profile});return {id:profile?.id||'created',revision:(profile?.revision||0)+1};},configure:async(profile,config)=>{calls.push({kind:'monitor',profile,config});if(failMonitor)throw Error('stale plugin configuration');}});
const values={name:'A',model:'manual-model',allowance_enabled:false,allowance_threshold:90,allowance_assistant:'assistant'};
await assert.rejects(persist(values,{request_id:'same-request'}),reason=>reason.uiKey==='providers.partialSave');
failMonitor=false;await persist(values,{request_id:'same-request'});
assert.equal(calls.filter(c=>c.kind==='connection').length,1,'A monitor-only retry cannot create a duplicate connection');
assert.equal(calls.at(-1).config.enabled,false,'Explicit reminder-off survives connection-save retries');
await persist({...values,name:'Edited after partial save'},{request_id:'edited-request'});
assert.equal(calls.filter(c=>c.kind==='connection').at(-1).profile.id,'created','Editing after a partial save updates the same acknowledged profile');
assert.equal(calls.at(-1).profile.revision,2);
assert(!Object.keys(calls[0].values).some(key=>key.startsWith('allowance_')),'Monitor fields cannot enter the connection write');

// A small DOM surface exercises the production disclosure, including pending
// locale changes, stale completion and disabled connection behavior.
class Element extends EventTarget {
 constructor(tag){super();this.tagName=tag;this.children=[];this.dataset={};this.attributes=new Map();this.isConnected=true;this.disabled=false;this.open=false;this._text='';}
 set textContent(value){this._text=String(value);this.children=[];}get textContent(){return this._text+this.children.map(c=>c.textContent).join('');}
 setAttribute(key,value){this.attributes.set(key,String(value));}getAttribute(key){return this.attributes.get(key);}
 append(...children){this.children.push(...children);}replaceChildren(...children){this._text='';this.children=children;}
 querySelector(selector){const [tag,cls]=selector.split('.');return this.children.find(node=>(!tag||node.tagName===tag)&&(!cls||(node.className||'').split(' ').includes(cls)))||this.children.map(node=>node.querySelector(selector)).find(Boolean)||null;}
 reportValidity(){return this.value!==''&&Number(this.value)>=1&&Number(this.value)<=100;}
 click(){if(!this.disabled)this.onclick?.();}
}
globalThis.document={createElement:tag=>new Element(tag)};
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject};},tick=()=>new Promise(resolve=>setTimeout(resolve,0));
let locale='en',enabled=true,current=true;const root=new Element('div'),pending=[],requests=[];
const render=createProviderAllowances({root,request:(path,options)=>{requests.push({path,options});const next=deferred();pending.push(next);return next.promise;},scope:{space:'one',owner:''},profileID:'profile',locale:()=>locale,current:()=>current,available:()=>enabled});
assert.equal(requests.length,0,'Collapsed quota does not make unnecessary reads');
const disclosure=root.children[0];disclosure.open=true;disclosure.dispatchEvent(new Event('toggle'));
assert.equal(requests.length,1);assert.match(requests[0].path,/profile_id=profile/);assert.equal(requests[0].options.space,'one');
locale='zh-CN';pending[0].resolve({status:'permission',snapshot:null,monitor:{enabled:false,threshold:90,assistant:'assistant',revision:1,status:'disabled'},assistants:[{id:'assistant',name:'Assistant'}]});await tick();
assert.match(root.textContent,/没有额度查询权限/,'Pending completion reads the current locale');assert.equal(disclosure.children[1].children[3].children[0].children[1].children[0].checked,false,'Read state preserves saved reminder-off');
enabled=false;render();const content=disclosure.children[1],refresh=content.children[1];assert.equal(refresh.disabled,true);refresh.click();assert.equal(requests.length,1,'Disabled provider cannot start new metadata work');
enabled=true;render();content.children[1].click();assert.equal(requests[1].options.body.action,'refresh');assert.equal(requests[1].options.body.profile_id,'profile');
const before=root.textContent;render.dispose();pending[1].resolve({status:'ready',snapshot:{read_at:'2026-01-01T00:00:00Z',accounts:[{name:'Late plan',windows:[]}]}});await tick();assert.equal(root.textContent,before,'Disposed connection cannot display a late quota result');
const unknownRoot=new Element('div');const unknown=createProviderAllowances({root:unknownRoot,request:async()=>({status:'ready',snapshot:{read_at:'2026-01-01T00:00:00Z',accounts:[{plan:'Plan A',windows:[{used:null},{used:0},{unlimited:true}]}]}}),scope:{},profileID:'other',locale:()=>locale});unknownRoot.children[0].open=true;unknownRoot.children[0].dispatchEvent(new Event('toggle'));await tick();assert.match(unknownRoot.textContent,/用量未知/);assert.match(unknownRoot.textContent,/已用 0%/);assert.match(unknownRoot.textContent,/无限制/);assert.match(unknownRoot.textContent,/Plan A/);unknown.dispose();
for(const key of ['providers.status.permission','providers.partialSave'])assert.notEqual(translate('en',key),translate('zh-CN',key));
console.log('Provider plugins: partial-save identity, explicit-off, lazy scoped quota, live locale, disabled connection and stale result fencing passed');

const desired={enabled:false,threshold:90,assistant:'assistant'};
const accepted=await saveAllowanceConfiguration({request:async(path,options)=>{if(options.method==='POST')throw Error('ack lost');return {monitor:{...desired,revision:8}};},scope:{space:'one'},profileID:'profile',values:desired,revision:7});
assert.equal(accepted.monitor.revision,8,'A lost acknowledgement can confirm the exact intent at a newer revision');
await assert.rejects(saveAllowanceConfiguration({request:async(path,options)=>{if(options.method==='POST')throw Error('conflict');return {monitor:{...desired,enabled:true,revision:9}};},scope:{},profileID:'profile',values:desired,revision:7}),/conflict/,'A different concurrent reminder configuration cannot be overwritten');
const now=Date.parse('2026-01-01T00:15:00Z'),fresh='2026-01-01T00:14:00Z',old='2025-12-31T23:00:00Z';
assert.equal(allowanceFreshness({}, {}, {read_at:fresh},now),'unknownObservation','Fetch time cannot impersonate source observation time');
assert.equal(allowanceFreshness({as_of:old,read_at:fresh},{},{read_at:fresh},now),'staleUsage','A recent source read cannot make old usage current');
assert.equal(allowanceFreshness({read_at:fresh},{resets_at:'2026-01-01T00:10:00Z'},{read_at:fresh},now),'staleUsage','Expired windows are historical even after a fresh fetch');
assert.equal(allowanceFreshness({read_at:fresh},{},{read_at:fresh},now),'');
assert.equal(allowanceFreshness({read_at:'2026-01-01T00:15:31Z'},{},{read_at:fresh},now),'staleUsage','Future source observations use the same bounded tolerance as the monitor');

const inlineMemory={view:{status:'ready',snapshot:null,monitor:{enabled:false,threshold:90,assistant:'assistant',revision:7},assistants:[{id:'assistant',name:'Assistant'}]},draft:{...desired},baseline:{...desired},revision:7};
const inlineRoot=new Element('div');let inlineRevision;
const inline=createProviderAllowances({root:inlineRoot,request:async(path,options)=>{if(options.method==='POST'&&options.body.action==='configure'){inlineRevision=options.body.revision;throw Error('revision conflict');}return {status:'ready',monitor:{enabled:true,threshold:75,assistant:'assistant',revision:8},assistants:[{id:'assistant',name:'Assistant'}]};},scope:{},profileID:'inline',locale:()=>locale,memory:inlineMemory});
const reminder=inlineRoot.children[0].children[1].children[3],thresholdInput=reminder.children[1].children[1].children[0];thresholdInput.value='95';thresholdInput.dispatchEvent(new Event('input'));
assert.equal(inlineMemory.draft.threshold,95);reminder.children[4].click();await tick();assert.equal(inlineRevision,7,'Inline edits submit the draft-captured revision');assert.equal(inlineMemory.draft.threshold,95);assert.match(inlineRoot.textContent,/保留/,'Conflicts retain the draft and show a localized error');
inline.dispose();const rebuilt=new Element('div');const replacement=createProviderAllowances({root:rebuilt,request:()=>assert.fail('Locale reconstruction must not write or replace a draft'),scope:{},profileID:'inline',locale:()=> 'en',memory:inlineMemory});assert.equal(inlineMemory.draft.threshold,95,'Page or locale reconstruction preserves unsaved inline values');assert.equal(inlineMemory.revision,7,'A newer read cannot silently rebase a dirty draft');assert.match(rebuilt.textContent,/Your input has been kept/);replacement.dispose();
console.log('Provider reminders: acknowledgement recovery, captured revision conflicts, source freshness, and draft reconstruction passed');

assert.deepEqual(providerConversationModels([{id:'chat',input_modalities:['text'],output_modalities:['text']},{id:'image',input_modalities:['text'],output_modalities:['image']},{id:'speech',input_modalities:['audio'],output_modalities:['text']},'manual']),[{id:'chat',input_modalities:['text'],output_modalities:['text']},'manual']);
assert.equal(providerConversationModels([{id:'current',output_modalities:['image']}],'current').length,1,'Refreshing catalog suggestions cannot overwrite the user-selected model');
