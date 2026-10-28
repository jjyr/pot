import assert from 'node:assert/strict';
import {createExtensions} from '../../web/src/components/extensions.mjs';

// A DOM double exercises callbacks and IO; native rendering is checked separately.
class Element extends EventTarget {
 constructor(tag){super();this.tagName=tag;this.children=[];this.dataset={};this.attributes=new Map();this.className='';this.disabled=false;this.open=false;this._text='';this.classList={contains:name=>this.className.split(' ').includes(name),add:name=>this.className+=' '+name,toggle:()=>{}};}
 set textContent(value){this._text=String(value);this.children=[];}get textContent(){return this._text+this.children.map(c=>c.textContent).join('');}
 setAttribute(key,value){this.attributes.set(key,String(value));}getAttribute(key){return this.attributes.get(key);}
 append(...children){for(const child of children){child.parent=this;this.children.push(child);}}
 replaceChildren(...children){this.children=[];this._text='';this.append(...children);}
 get options(){return this.children;}get isConnected(){return this===document.body||Boolean(this.parent?.isConnected);}
 matches(selector){if(selector.startsWith('[data-')){const name=selector.slice(6,-1).replace(/-([a-z])/g,(_,c)=>c.toUpperCase());return name in this.dataset;}return selector.startsWith('.')?this.classList.contains(selector.slice(1)):this.tagName===selector;}
 querySelectorAll(selector){return this.children.flatMap(child=>[...(selector.split(',').some(s=>child.matches(s))?[child]:[]),...child.querySelectorAll(selector)]);}
 closest(selector){return this.matches(selector)?this:this.parent?.closest(selector);}
 querySelector(selector){return this.querySelectorAll(selector)[0]||null;}
 showModal(){this.open=true;}close(){this.open=false;this.dispatchEvent(new Event('close'));}
 remove(){if(this.parent)this.parent.children=this.parent.children.filter(child=>child!==this);this.parent=null;}
 focus(){document.activeElement=this;}click(){if(!this.disabled)return this.onclick?.();}
}
globalThis.document={createElement:tag=>new Element(tag),createElementNS:(_,tag)=>new Element(tag)};document.body=new Element('body');
globalThis.window=new EventTarget();window.confirm=()=>{throw Error('Browser confirm is unavailable in native WKWebView');};
let locale='en',items=[{id:'fixture',name:'Fixture MCP',transport:'http',url:'http://127.0.0.1:18744',enabled:true,revision:0}],posts=[];
const root=new Element('main');document.body.append(root);
const component=createExtensions({root,locale:()=>locale,client:{runtime:{runtime_id:'extensions-fixture',boot_id:'boot'},scope:()=>({space:'local',owner:'main'}),request:async(path,options={})=>{if(options.method==='POST'){posts.push(options);if(options.body.action==='remove')items=[];else {const server={...options.body.server,revision:(options.body.server.revision||0)+1};items.push(server);return server;}return {};}return {items:[],mcp:items};}}});
const button=(host,key)=>host.querySelectorAll('button').find(node=>node.dataset.extensionText===key);
const editor=()=>document.body.querySelector('.extensions-dialog'),confirmation=()=>document.body.querySelector('.extension-confirmation');
await component.load();root.querySelector('.extension-open').click();
let pending=button(editor(),'remove').click();assert(confirmation().open);assert.equal(posts.length,0);button(confirmation(),'cancel').click();await pending;assert.equal(posts.length,0);assert(editor().open);
pending=button(editor(),'remove').click();confirmation().dispatchEvent(new Event('cancel',{cancelable:true}));await pending;assert.equal(posts.length,0,'Escape cannot remove an extension');
pending=button(editor(),'remove').click();locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));assert.match(confirmation().textContent,/移除 Fixture MCP/);assert.equal(document.activeElement.dataset.extensionText,'cancel');button(confirmation(),'remove').click();await pending;assert.equal(posts.length,1);assert.equal(posts[0].body.action,'remove');assert.equal(editor(),null);
// Import cancellation keeps the JSON draft, and confirmation submits exactly once.
root.querySelectorAll('button').find(node=>node.getAttribute('aria-label')==='添加 MCP').click();await button(editor(),'importJSON').click();
const input=editor().querySelector('textarea');input.value='{"mcpServers":{"imported":{"url":"http://127.0.0.1:18744"}}}';editor().dispatchEvent(new Event('input'));
pending=button(editor(),'import').click();assert.match(confirmation().textContent,/1/);button(confirmation(),'cancel').click();await pending;assert.equal(input.value.includes('imported'),true);assert.equal(posts.length,1);
pending=button(editor(),'import').click();const duplicate=button(editor(),'import').click();await duplicate;button(confirmation(),'import').click();await pending;assert.equal(posts.length,2);assert.equal(posts[1].body.server.id,'imported');assert.equal(posts[1].space,'local');assert.equal(posts[1].body.credentials,undefined,'Unauthenticated imports need no credential write');assert.equal(editor(),null);
// Dirty navigation is cancellable and disposal settles pending confirmations.
root.querySelectorAll('button').find(node=>node.getAttribute('aria-label')==='添加 MCP').click();editor().dispatchEvent(new Event('input'));
pending=component.leave();button(confirmation(),'cancel').click();assert.equal(await pending,false);assert(editor().open);
pending=component.leave();button(confirmation(),'discardChanges').click();assert.equal(await pending,true);assert.equal(editor(),null);
root.querySelectorAll('button').find(node=>node.getAttribute('aria-label')==='添加 MCP').click();editor().dispatchEvent(new Event('input'));
pending=component.leave();component.dispose();assert.equal(await pending,false);assert.equal(confirmation(),null);assert.equal(editor(),null);assert.equal(posts.length,2);
// A retained existing row with revision zero is not a creation request. If it
// disappears before Save, live confirmation must not recreate it.
const removedRoot=new Element('main');document.body.append(removedRoot);let removedItems=[{id:'zero',name:'Revision zero',revision:0,transport:'http',url:'https://fixture.invalid',enabled:true}],removedPosts=0;
const removed=createExtensions({root:removedRoot,locale:()=> 'en',client:{runtime:{id:'zero-fixture',boot_id:'boot'},scope:()=>({space:'zero',owner:'owner'}),request:async(path,options={})=>{if(options.method==='POST'){removedPosts++;return {};}return {items:[],mcp:removedItems};}}});
await removed.load();removedRoot.querySelector('.extension-open').click();await button(editor(),'edit').click();
removedItems=[];await button(editor(),'save').click();assert.equal(removedPosts,0,'Deleted existing revision-zero server cannot be recreated by a stale editor');assert(editor(),'Failed confirmation preserves the editor');removed.dispose();
console.log('Extensions: explicit import/remove/discard, cancel/Escape, duplicate actions, live locale, draft retention, deleted revision-zero fencing and disposal passed');
