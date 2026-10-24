import assert from 'node:assert/strict';
import {createPluginNotifications} from '../../web/src/components/plugin-notifications.mjs';
import {translate} from '../../web/src/locales/messages.mjs';
class Element {
 constructor(tag){this.tagName=tag;this.children=[];this.dataset={};this.attributes=new Map();this.disabled=false;this._text='';}
 set textContent(value){this._text=String(value);this.children=[];}get textContent(){return this._text+this.children.map(c=>c.textContent).join('');}
 setAttribute(key,value){this.attributes.set(key,String(value));}getAttribute(key){return this.attributes.get(key);}
 append(...children){this.children.push(...children);}
 querySelectorAll(selector){const tags=selector.split(',');return this.children.flatMap(child=>[...(tags.includes(child.tagName)?[child]:[]),...child.querySelectorAll(selector)]);}
}
globalThis.document={createElement:tag=>new Element(tag)};
const account={identity:{id:'123'}},fields=['repositories','reasons','subject_types','threads'].map(key=>({key,label:'pluginNotifications.'+key,...(key==='reasons'?{options:['mention','subscribed','review_requested'].map(value=>({value,label:'pluginNotifications.reason.'+value}))}:{})}));
let data={items:[{id:'github',settings:{revision:1,fields,subscriptions:[{id:'default',enabled:true,assistant:'main',revision:1,status:'ready',filters:{repositories:null,reasons:['mention'],subject_types:null,threads:null}}]}}],assistants:[{id:'main',name:'Pot'},{id:'B',name:'B'},{id:'C',name:'C'}]};
const calls=[];let locale='en',root,resolve,reject;
const settings=()=>data.items[0].settings;
const form=createPluginNotifications({pluginID:'github',request:async(path,options)=>{calls.push(options.body);return new Promise((yes,no)=>{resolve=yes;reject=no;});},scope:{},signal:new AbortController().signal,locale:()=>locale,refresh:async()=>{},render:()=>{root=form.form();}});
const render=()=>{root=form.form();},control=(key,tag='input')=>root.querySelectorAll(tag).find(i=>i.getAttribute('aria-label')===translate(locale,'pluginNotifications.'+key).replace('{field}',translate(locale,'pluginNotifications.repositories'))),button=key=>root.querySelectorAll('button').find(i=>i.textContent===translate(locale,'pluginNotifications.'+key));
form.update(data,account);render();assert(control('reason.mention').checked);
control('repositories','select').onchange({currentTarget:{value:'selected'}});control('values').oninput({currentTarget:{value:'me/repo, me/other'}});
control('assistant','select').onchange({currentTarget:{value:'C'}});
// Background edits and locale changes retain the original draft and revision.
settings().revision=2;settings().subscriptions[0].revision=2;settings().subscriptions[0].filters.reasons=['review_requested'];form.update(data,account);locale='zh-CN';render();assert.equal(control('values').value,'me/repo, me/other');assert(control('reason.mention').checked);
let pending=button('save').onclick();assert.equal(calls[0].tool,'update_subscription');assert.equal(calls[0].arguments.revision,1);assert.equal(calls[0].arguments.assistant,'C');assert.deepEqual(calls[0].arguments.filters.repositories,['me/repo','me/other']);assert(button('save').disabled);
reject(Error('stale revision'));await pending;assert.match(root.textContent,/草稿已保留/);assert.equal(control('values').value,'me/repo, me/other');
pending=button('save').onclick();assert.equal(calls[1].arguments.request_id,calls[0].arguments.request_id);reject(Error('stale revision'));await pending;
button('reset').onclick();assert(control('reason.review_requested').checked);
control('reason.review_requested').onchange({currentTarget:{checked:false}});pending=button('save').onclick();assert.deepEqual(calls.at(-1).arguments.filters.reasons,[]);
locale='en';const saved=structuredClone(data);saved.items[0].settings.revision=3;saved.items[0].settings.subscriptions[0]={...saved.items[0].settings.subscriptions[0],...calls.at(-1).arguments,revision:3};resolve(saved);await pending;data=saved;assert.match(root.textContent,/Revision: 3/);
// Late save completions cannot overwrite newer MCP projections.
control('reason.mention').onchange({currentTarget:{checked:true}});pending=button('save').onclick();const latest=structuredClone(saved);latest.items[0].settings.revision=5;latest.items[0].settings.subscriptions[0].revision=5;form.update(latest,account);resolve({...saved,items:[{...saved.items[0],settings:{...saved.items[0].settings,revision:4}}]});await pending;assert.match(root.textContent,/stale_notification_response/);assert(control('reason.mention').checked);assert.match(root.textContent,/Revision: 5/);
// A new repository subscription starts with explicit unrestricted reasons.
button('add').onclick();const saves=root.querySelectorAll('button').filter(i=>i.textContent===translate(locale,'pluginNotifications.save'));pending=saves.at(-1).onclick();assert.equal(calls.at(-1).tool,'create_subscription');assert.equal(calls.at(-1).arguments.filters.reasons,null);reject(Error('fixture failure'));await pending;
// Delete uses only ID, revision and request identity.
pending=button('delete').onclick();assert.equal(calls.at(-1).tool,'delete_subscription');assert.deepEqual(Object.keys(calls.at(-1).arguments).sort(),['id','request_id','revision']);reject(Error('fixture failure'));await pending;
// Account replacement clears all old drafts.
form.update(data,{identity:{id:'456'}});render();assert(!root.textContent.includes('stale_notification_response'));assert(!root.textContent.includes('New subscription'));
console.log('GitHub subscriptions UI: CRUD, multi-repository recipients, null/empty filters, drafts, locale, receipts and stale completions passed');
