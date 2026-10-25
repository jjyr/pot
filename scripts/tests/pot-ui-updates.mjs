import assert from 'node:assert/strict';
import {createUpdates} from '../../web/src/components/updates.mjs';

class Element extends EventTarget {
  dataset={};children=[];attributes=new Map();open=false;disabled=false;hidden=false;textContent='';
  setAttribute(name,value){this.attributes.set(name,String(value));}
  getAttribute(name){return this.attributes.get(name);}
  append(...nodes){this.children.push(...nodes);}
  showModal(){this.open=true;}close(){this.open=false;}focus(){}remove(){}
  click(){if(!this.disabled&&!this.hidden)return this.onclick?.();}
}
globalThis.document={createElement:()=>new Element(),createElementNS:()=>new Element()};
globalThis.window=new EventTarget();
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
let locale='en',value={auto_install:true,current:'0.1.0',version:'0.2.0',phase:'ready',work:{tasks:2,replies:0,sessions:0}},calls=[],reloads=0;
const client={request:async(path,options)=>{if(options.method==='POST'){calls.push(options);return {...value,phase:'installing'};}return structuredClone(value);}};
const create=client=>createUpdates({client,locale:()=>locale,changed:()=>{},notify:message=>assert.fail(message),reload:()=>reloads++});
const updates=create(client);
await updates.refresh({space:'control-a'});assert.equal(updates.button().getAttribute('aria-label'),'New version 0.2.0');
updates.button().click();assert(updates.dialog.open);assert.equal(calls.length,0);
const [title,body,release,actions]=updates.dialog.children;
assert.equal(release.href,'https://github.com/jjyr/pot/releases/tag/v0.2.0');
actions.children[1].click();await tick();assert(updates.dialog.open);assert.match(title.textContent,/interrupt/);assert.equal(calls.length,0,'Opening interruption confirmation cannot stop work');
actions.children[0].click();assert(!updates.dialog.open);assert.equal(calls.length,0);
updates.button().click();actions.children[1].click();locale='zh-CN';window.dispatchEvent(new Event('pot:locale'));assert.match(title.textContent,/中断/);assert.match(body.textContent,/2 项工作/);
actions.children[1].click();await tick();assert.equal(calls.length,1);assert.equal(calls[0].space,'control-a');assert.equal(calls[0].body.interrupt,true);
updates.button().click();assert.equal(actions.children[1].hidden,true,'In-progress details cannot install twice');assert.equal(release.href,'https://github.com/jjyr/pot/releases/tag/v0.2.0');
value={...value,current:'0.2.0',phase:'current'};await updates.refresh();assert.equal(reloads,1);assert.equal(updates.button(),null);updates.close();
value={...value,current:'0.1.0',phase:'ready',work:{tasks:0}};calls=[];
const idle=create(client);await idle.refresh({space:'control-b'});idle.button().click();idle.dialog.children[3].children[1].click();await tick();assert(!idle.dialog.open);assert.equal(calls.length,1);assert.equal(calls[0].body.interrupt,false);idle.close();
for(const reason of ['format','protocol','migration_path','artifact','installation','no_key','permissions','verification','recovery']){
  value={current:'0.1.0',version:'0.2.0',auto_install:false,reason,phase:reason==='recovery'?'blocked':'available'};
  const manual=create(client);await manual.refresh({space:'manual'});assert(manual.button(),`${reason} release must be visible`);manual.button().click();assert.equal(manual.dialog.children[2].href,'https://github.com/jjyr/pot/releases/tag/v0.2.0');assert.equal(manual.dialog.children[3].children[1].hidden,true);assert(!manual.dialog.children[1].textContent.startsWith('updates.'),'Reason must be translated');manual.close();
}
let finishA,finishB;
const scoped=create({request:(_,options)=>new Promise(resolve=>{if(options.space==='a')finishA=resolve;else finishB=resolve;})});
const old=scoped.refresh({space:'a'}),fresh=scoped.refresh({space:'b'});finishB({current:'0.1.0'});await fresh;finishA(value);await old;assert.equal(scoped.button(),null,'A late result cannot expose another Control update');scoped.close();
let finishCheck,checks=0,fail=false;
value={current:'0.1.0',version:'',discovery:'current'};
const checking=create({request:async(_,options)=>{if(options.method!=='POST')return value;assert.equal(options.body.action,'check');assert.equal(options.space,'check-control');checks++;if(fail)throw Error('offline');return new Promise(resolve=>finishCheck=resolve);}});
await checking.refresh({space:'check-control'});let observed;const stop=checking.watch({space:'check-control'},snapshot=>observed=snapshot);
const first=checking.check(),second=checking.check();assert.equal(checks,1);assert(observed.checking);finishCheck(value);await Promise.all([first,second]);assert(!observed.checking);assert.equal(observed.feedback,'current');
const third=checking.check();finishCheck({...value,version:'0.3.0',discovery:'manual',auto_install:false,reason:'verification'});await third;assert.equal(observed.data.version,'0.3.0');assert.equal(observed.feedback,'found');
fail=true;await checking.check();assert.equal(observed.feedback,'checkFailed');assert.equal(observed.data.version,'0.3.0','A failed check keeps the known release');
fail=false;const retired=checking.check();await checking.refresh({space:'another-control'});finishCheck({...value,version:'9.0.0'});await retired;assert.equal(checking.button(),null,'Late manual check must not leak across Controls');stop();checking.close();
console.log('Updates: manual network command, shared check, stale scopes, release links/reasons, locale, confirmation and restart checks passed');
