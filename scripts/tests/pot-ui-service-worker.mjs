import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import {webcrypto,createHash} from 'node:crypto';
const hash=value=>createHash('sha256').update(value).digest('hex');
const config={version:'fixture',assets:[{url:'/index.html',sha256:hash('current UI')},{url:'/app.js',sha256:hash('current JS')}]};
const current='pot-ui-shell-fixture',retired='retired-product-ui';
const entries=new Map([[retired,new Map([['/app.js','retired JS']])],[current,new Map([['/index.html','current UI'],['/app.js','current JS']])]]);
const listeners=new Map(),reads=[],writes=[];let network;
const pathOf=value=>new URL(typeof value==='string'?value:value.url,'https://pot.test').pathname;
const caches={async keys(){return [...entries.keys()]},async delete(name){return entries.delete(name)},async open(name){if(!entries.has(name))entries.set(name,new Map());return {
 async match(input){reads.push(name);const body=entries.get(name).get(pathOf(input));return body===undefined?undefined:new Response(body)},
 async put(input,response){writes.push([name,pathOf(input)]);entries.get(name)?.set(pathOf(input),await response.text())},
}}};
const installation=new Map(),displayed=[],focused=[],opened=[];
const context=vm.createContext({
 notificationInstallation:async work=>work({get:async key=>installation.get(key),put:(value,key)=>installation.set(key,value),remove:key=>installation.delete(key)}),
 validNotificationTarget:event=>event&&typeof event.id==='string'&&['space','control','owner'].every(key=>event[key])&&['reply','task-complete','task-failed','approval'].includes(event.kind),
 URL,Promise,caches,crypto:webcrypto,Response,
 fetch:async()=>{if(!network)throw Error('offline fixture');return new Response(network.body,{status:network.status})},
 self:{registration:{showNotification:async(title,options)=>displayed.push({title,options})},clients:{matchAll:async()=>[],openWindow:async url=>opened.push(url)},location:{origin:'https://pot.test'},addEventListener:(name,handler)=>listeners.set(name,handler)},
});
vm.runInContext(fs.readFileSync(new URL('../../web/src/service-worker.js',import.meta.url),'utf8').replace(/^import[^\n]*\n/,'').replace('/* POT_SHELL_MANIFEST */ null',JSON.stringify(config)),context);
async function request(path,mode='cors',method='GET'){let response;listeners.get('fetch')({request:{method,url:new URL(path,'https://pot.test').href,mode},respondWith(value){response=value}});return response;}
for(const [path,mode,expected] of [['/index.html','navigate','current UI'],['/app.js','cors','current JS']])assert.equal(await(await request(path,mode)).text(),expected);
network={status:200,body:'new version JS'};
assert.equal(await(await request('/app.js')).text(),'current JS','installed version must win over network');
entries.get(current).delete('/app.js');assert.equal((await request('/app.js')).type,'error','a missing asset cannot mix newer network content');
network={status:200,body:'current JS'};assert.equal(await(await request('/app.js')).text(),'current JS','an evicted asset repairs only against its pinned digest');
entries.get(current).set('/app.js','corrupt');network=null;assert.equal((await request('/app.js')).type,'error','corrupt cached code must never execute');
entries.get(current).set('/app.js','current JS');
for(const [path,mode,method] of [['/api/v1/messages','cors','GET'],['/access/token','navigate','GET'],['/wails/runtime.js','cors','GET'],['https://other.test/app.js','cors','GET'],['/app.js','cors','POST'],['/unrelated.txt','cors','GET']])assert.equal(await request(path,mode,method),undefined);
assert(reads.every(name=>name===current));
assert.equal(entries.get(retired).get('/app.js'),'retired JS');
console.log('PASS installed-version cache, integrity, exact-version repair and network exclusions');
const target={id:'reply',space:'node',control:'node',owner:'alpha',task:'task-one',kind:'reply'};
const payload={version:1,event:target,device:'device',title:'Pot · Alpha',body:'Synthetic reply'};
installation.set('device',{schema_version:1,id:'device',control:'node',enabled:true,channel:'push'});
async function dispatch(name,event){let pending;listeners.get(name)({...event,waitUntil:value=>pending=value});await pending}
await dispatch('push',{data:{json:()=>({...payload,device:'other'})}});assert.equal(displayed.length,0);
await dispatch('push',{data:{json:()=>payload}});assert.equal(displayed.length,1);assert.equal(displayed[0].options.tag,'reply');
const notification={data:{device:'device',event:{...target,url:'https://attacker.test/'}},close(){}};
await dispatch('notificationclick',{notification});assert.deepEqual(opened,['/'],'worker opens only its own fixed root');
assert.equal(installation.get('click').event.task,'task-one');
context.self.clients.matchAll=async()=>[{url:'https://pot.test/',focus:async()=>focused.push('focus'),postMessage:message=>focused.push(message.type)}];
await dispatch('notificationclick',{notification});assert.deepEqual(focused,['focus','pot:notification-click']);
installation.get('device').enabled=false;await dispatch('push',{data:{json:()=>payload}});assert.equal(displayed.length,1);
await dispatch('notificationclick',{notification});assert.equal(focused.length,2,'disabled installation cannot restore a target');
console.log('Push display, device fencing, fixed click destination and pending exact Task target passed');
