import assert from 'node:assert/strict';
import {createUpdates} from '../../web/src/components/updates.mjs';

class Element extends EventTarget {
  dataset={};children=[];attributes=new Map();open=false;disabled=false;textContent='';
  setAttribute(name,value){this.attributes.set(name,String(value));}
  getAttribute(name){return this.attributes.get(name);}
  append(...nodes){this.children.push(...nodes);}
  showModal(){this.open=true;}
  close(){this.open=false;}
  focus(){}
  remove(){}
  click(){if(!this.disabled)this.onclick?.();}
}
globalThis.document={createElement:()=>new Element(),createElementNS:()=>new Element()};
globalThis.window=new EventTarget();
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
let locale='en',value={supported:true,current:'0.1.0',version:'0.2.0',phase:'ready',work:{tasks:2,replies:0,sessions:0}},calls=[],reloads=0;
const client={request:async(path,options)=>{if(options.method==='POST'){calls.push(options);return {...value,phase:'installing'};}return structuredClone(value);}};
const updates=createUpdates({client,locale:()=>locale,changed:()=>{},notify:message=>assert.fail(message),reload:()=>reloads++});
await updates.refresh({space:'control-a'});assert.equal(updates.button().getAttribute('aria-label'),'Upgrade to 0.2.0');
updates.button().click();await tick();assert(updates.dialog.open);assert.equal(calls.length,0,'Confirmation alone cannot interrupt work');
const [title,body,actions]=updates.dialog.children;actions.children[0].click();assert(!updates.dialog.open);assert.equal(calls.length,0);
updates.button().click();locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));assert.match(title.textContent,/中断/);assert.match(body.textContent,/2 项工作/);
actions.children[1].click();await tick();assert.equal(calls.length,1);assert.equal(calls[0].space,'control-a');assert.equal(calls[0].body.interrupt,true);assert.equal(updates.button().disabled,true,'Duplicate clicks stay disabled');
value={...value,current:'0.2.0',phase:'current'};await updates.refresh();assert.equal(reloads,1);assert.equal(updates.button(),null);updates.close();
value={...value,current:'0.1.0',phase:'ready',work:{tasks:0}};calls=[];
const idle=createUpdates({client,locale:()=>locale,changed:()=>{},notify:message=>assert.fail(message)});await idle.refresh({space:'control-b'});idle.button().click();await tick();assert(!idle.dialog.open);assert.equal(calls.length,1);assert.equal(calls[0].body.interrupt,false);idle.close();
let finishA,finishB;
const scoped=createUpdates({client:{request:(_,options)=>new Promise(resolve=>{if(options.space==='a')finishA=resolve;else finishB=resolve;})},locale:()=>locale,changed:()=>{},notify:()=>{}});
const old=scoped.refresh({space:'a'}),fresh=scoped.refresh({space:'b'});finishB({supported:false});await fresh;finishA(value);await old;assert.equal(scoped.button(),null,'A late result cannot expose another Control update');scoped.close();
console.log('Updates: explicit interruption, cancellation, idle upgrade, duplicate actions, locale switch, restart reload and Control fencing passed');
