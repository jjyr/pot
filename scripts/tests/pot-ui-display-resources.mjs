import {createMemoryProjectionStore} from '../../web/src/state/projection-store.mjs';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createProductionClient} from '../../web/src/adapters/production.mjs';
import {installWSFixture} from './helpers/ws-fixture.mjs';
installWSFixture();
import {createDisplayResources,resourcePresentation,projectResourceMessages} from '../../web/src/state/display-resources.mjs';
import {projectChatReplies} from '../../web/src/state/chat-replies.mjs';
import {resourcePreviewKind} from '../../web/src/components/resource-view.mjs';

for(const [mime_type,entry,name,kind] of [
  ['text/plain','review.md','review.html','markdown'],
  ['text/plain','review.MARKDOWN','review.txt','markdown'],
  ['text/html','raw.md','raw.html','markdown'],
  ['text/markdown; charset=utf-8','opaque','opaque','markdown'],
  ['text/x-markdown','opaque','opaque','markdown'],
  ['text/javascript','source.js','misleading.html','text'],
  ['text/css','style.css','misleading.html','text'],
  ['application/octet-stream','binary.js','misleading.md','file'],
  ['text/plain','ordinary.txt','spoof.md','text'],
  ['text/plain','ordinary.log','spoof.markdown','text'],
  ['application/pdf','binary.md','spoof.md','pdf'],
  ['text/html','actual.html','spoof.md','html'],
])assert.equal(resourcePreviewKind({mime_type,entry,name}),kind,`${mime_type} ${entry}`);

const file={path:'report.txt',mime_type:'text/plain',size:6,digest:'fixed'},resource={id:'resource',name:'report.txt',entry:file.path,files:[file],download:file},scope={owner:'alice',space:'remote'},reads=[];
const acquire=createDisplayResources(async(path,options)=>{reads.push({path,options});return {size:file.size,revision:file.digest,offset:0,data:btoa('report')};},()=>scope,(_,path,captured)=>JSON.stringify([captured,path]));
const first=acquire(resource),second=acquire(resource);scope.owner='bob';scope.space='local';
assert.equal(await first.read(file.path),await second.read(file.path));assert.equal(reads.length,1);assert.equal(reads[0].options.owner,'alice');assert.equal(reads[0].options.space,'remote');assert.match(first.url(file.path),/alice/);
first.release();await Promise.resolve();assert(!reads[0].options.signal.aborted);second.release();await Promise.resolve();assert(reads[0].options.signal.aborted);
const next=acquire(resource);await next.read(file.path);assert.equal(reads[1].options.owner,'bob');next.release();
const message={id:'tool',tool:{name:'present_resource',phase:'completed',result:{presentation:{resource,mode:'preview'}}}};
assert(resourcePresentation(message));assert.equal(projectResourceMessages([message])[1].presentation.resource.id,'resource');
assert.equal(resourcePresentation({...message,tool:{...message.tool,phase:'failed'}}),null);
assert.equal(resourcePresentation({...message,tool:{...message.tool,name:'get_screenshot'}}),null,'Only explicit presentation calls become replies');
const persisted={id:'reply',role:'assistant',status:'complete',text:'',presentation:message.tool.result.presentation};
assert.equal(projectChatReplies({messages:[persisted]},null)[0].presentation,persisted.presentation,'Resource-only replies survive projection without prose');
const completedResource=projectChatReplies({messages:[{id:'input',role:'user',text:'Show the report'}, {...persisted,reply_to:'input'}],replies:[{input_id:'input',state:'completed'}]});
assert.equal(completedResource[1].presentation,persisted.presentation,'Completed resource-only replies remain visible while empty results settle silently');
assert.equal(completedResource[1].phase,'complete');
const broken=createDisplayResources(async()=>({size:6,revision:'different',offset:0,data:btoa('report')}),()=>scope,()=>'/content')(resource);
await assert.rejects(broken.read(file.path));broken.release();

// Both adapters use the server's download basename, independently of the label.
const labelled={...resource,name:'Pot A/B \\ '+ '中文'.repeat(300),download:{...file,path:'Pot A_B _.zip'}};
const labelledMessage={...message,tool:{...message.tool,result:{presentation:{resource:labelled,mode:'download'}}}};
assert.equal(projectResourceMessages([labelledMessage])[1].presentation.resource.name,labelled.name);
const previousFetch=globalThis.fetch,previousDocument=globalThis.document,requests=[],anchors=[],nativeCalls=[];
globalThis.fetch=async url=>{requests.push(String(url));return {ok:true,status:200,json:async()=>({runtime_id:'fixture'})};};
globalThis.document={createElement:()=>({click(){anchors.push({...this});},remove(){}}),body:{append(){}}};
globalThis.resourceDownloadNativeCall=async(method,...args)=>{
  nativeCalls.push({method,args});
  return method.endsWith('.Call')?{runtime_id:'fixture'}:method.endsWith('.AssistantReads')?[]:method.endsWith('.Transport')?{url:'ws://localhost/api/v1/ws',token:'fixture'}:true;
};
const hooks=registerHooks({resolve(specifier,context,next){
  if(specifier==='/wails/runtime.js')return {url:'data:text/javascript,export const Call={ByName:(...args)=>globalThis.resourceDownloadNativeCall(...args)};',shortCircuit:true};
  return next(specifier,context);
}});
try{
  const browser=await createProductionClient({resourceStorage:createMemoryProjectionStore()});browser.setScope({owner:'alice',space:'remote'});
  const desktop=await createProductionClient({desktop:true,resourceStorage:createMemoryProjectionStore()});desktop.setScope({owner:'alice',space:'remote'});
  for(const value of [labelled,{...labelled,download:file}]){
    await browser.downloadResource(value);await desktop.downloadResource(value);
    assert.equal(anchors.at(-1).download,value.download.path);
    assert.match(anchors.at(-1).href,/assistant_id=alice&space_id=remote/);
    assert.match(requests.at(-1),/path=%40download/);
    assert.deepEqual(nativeCalls.at(-1),{method:'github.com/jjyr/pot/internal/desktop.Service.DownloadResource',args:[value.id,value.download.path,'alice','remote','@download']});
  }
}finally{
  hooks.deregister();globalThis.fetch=previousFetch;globalThis.document=previousDocument;delete globalThis.resourceDownloadNativeCall;
}
console.log('Display resources: immutable identity, captured owner/Space, sharing/disposal, explicit intent and resource-only replies passed');
