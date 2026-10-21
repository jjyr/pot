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
const account={identity:{id:'123'}},base={items:[{id:'github',enabled:true,assistant:'main',revision:1,status:'ready',config:{account_id:'123',repositories:null,reasons:['mention'],subject_types:null,threads:null}}],assistants:[{id:'main',name:'Pot'}]};
const calls=[];let locale='en',data=structuredClone(base),root,resolve,reject;
const form=createPluginNotifications({request:async(path,options)=>{calls.push(options.body);return new Promise((yes,no)=>{resolve=yes;reject=no;});},scope:{},signal:new AbortController().signal,locale:()=>locale,refresh:async()=>{},render:()=>{root=form.form();}});
const render=()=>{root=form.form();},control=(key,tag='input')=>root.querySelectorAll(tag).find(i=>i.getAttribute('aria-label')===translate(locale,'pluginNotifications.'+key).replace('{field}',translate(locale,'pluginNotifications.repositories'))),save=()=>root.querySelectorAll('button').find(i=>i.textContent===translate(locale,'pluginNotifications.save'));
form.update(data,account);render();assert(control('reason.mention').checked);assert(!control('reason.subscribed').checked);
control('repositories','select').onchange({currentTarget:{value:'selected'}});control('values').oninput({currentTarget:{value:'me/repo'}});
// A background/tool refresh and locale switch cannot replace dirty controls.
data.items[0].revision=2;data.items[0].config.reasons=['review_requested'];form.update(data,account);locale='zh-CN';render();assert.equal(control('values').value,'me/repo');assert(control('reason.mention').checked);
let pending=save().onclick();assert.equal(calls[0].revision,1);assert.equal(calls[0].config.repositories[0],'me/repo');assert.equal(calls[0].config.subject_types,null);assert(save().disabled);
reject(Error('stale revision'));await pending;assert.match(root.textContent,/草稿已保留/);assert.equal(control('values').value,'me/repo');
// Identical retry preserves request identity and revision.
pending=save().onclick();assert.equal(calls[1].request_id,calls[0].request_id);reject(Error('stale revision'));await pending;
// Discarding via account change starts a fresh revision and receipt.
form.update(data,{identity:{id:'456'}});form.update(data,account);render();assert(control('reason.review_requested').checked);
control('reason.review_requested').onchange({currentTarget:{checked:false}});pending=save().onclick();assert.deepEqual(calls.at(-1).config.reasons,[]);assert.notEqual(calls.at(-1).request_id,calls[0].request_id);
locale='en';const saved=structuredClone(data);saved.items[0]={...saved.items[0],...calls.at(-1),revision:3};resolve(saved);await pending;assert.match(root.textContent,/Revision: 3/);assert.match(root.textContent,/No values/);
// A late save cannot overwrite a newer tool projection.
control('reason.mention').onchange({currentTarget:{checked:true}});pending=save().onclick();const latest=structuredClone(saved);latest.items[0].revision=5;form.update(latest,account);resolve({...saved,items:[{...saved.items[0],revision:4}]});await pending;assert.match(root.textContent,/stale_notification_response/);assert(control('reason.mention').checked);assert.match(root.textContent,/Revision: 5/);
console.log('GitHub filters UI: shared projections, null/empty, dirty drafts, locale, receipts, conflicts and stale completions passed');
