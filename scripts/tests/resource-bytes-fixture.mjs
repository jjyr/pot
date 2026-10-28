import {createProductionClient} from '/adapters/production.mjs';
import {createResourceCopies} from '/state/resource-bytes.mjs';
import {createResourceView} from '/components/resource-view.mjs';
import {renderMessageAttachments} from '/components/attachments.mjs';
import {resourceBytesContract} from './resource-bytes-contract.mjs';
const results=document.querySelector('#results'),log=label=>{results.textContent+='\nPASS '+label;},params=new URLSearchParams(location.search),offline=params.has('offline');
window.fixtureErrors=[];window.addEventListener('error',event=>fixtureErrors.push(event.message));window.addEventListener('unhandledrejection',event=>fixtureErrors.push(event.reason?.message||String(event.reason)));
try{
 if(!offline&&!params.has('skipContract')){results.textContent='Real IndexedDB contract';await resourceBytesContract(log);}else results.textContent='Offline display validation';
 const data=JSON.parse(localStorage.getItem('resource-fixture-data')||'null')||await(await fetch('/fixture-data')).json();if(!offline)localStorage.setItem('resource-fixture-data',JSON.stringify(data));
 const copies=createResourceCopies({name:'synthetic-resource-reading'}),client=await createProductionClient({startupIdentity:{runtime_id:'synthetic-host',boot_id:'synthetic-boot'},resourceCopies:copies});
 client.onlineReady=true;client.spaces=[{id:'local',control_id:'synthetic-control'}];client.assistants=[{id:'alice',space_id:'local'}];client.setScope({space:'local',owner:'alice'});
 const scope=client.scope(),cards=document.querySelector('#cards');let mounted=false;
 const mount=async()=>{
  if(mounted){cards.replaceChildren();mounted=false;await Promise.resolve();return;}
  const rows=await client.messageFiles('message',{sourceText:'synthetic saved message'});
  if(rows.length!==5)throw Error('Expected five ready immutable bindings');
  for(const row of rows)cards.append(createResourceView({resource:row.resource,mode:'preview'},{acquireResource:resource=>client.acquireResource(resource,scope),downloadResource:resource=>client.downloadResource(resource,scope)}));
  cards.append(renderMessageAttachments([{id:'sent-image',name:'sent.png',mime_type:'image/png',size:atob(data.bytes['picture.png']).length}],{client,scope,locale:()=>document.documentElement.lang}));mounted=true;
 };
 document.querySelector('#unmount').onclick=()=>mount().catch(error=>{results.textContent+='\nFAIL '+error.stack;window.fixtureFailure=error.message;});document.querySelector('#locale').onclick=()=>{document.documentElement.lang=document.documentElement.lang==='en'?'zh-CN':'en';window.dispatchEvent(new Event('pot:locale'));};
 window.resourceFixture={client,copies,data,mount,scope,log};await mount();
 if('serviceWorker'in navigator){await navigator.serviceWorker.register('/service-worker.js',{type:'module'});await navigator.serviceWorker.ready;}
 document.querySelector('#banner').textContent=offline?'AFTER — previously committed resources reopened with the browser offline.':'ONLINE — successful authorized reads commit independent display copies.';
 window.fixtureReady=true;log('ready bindings restored using complete source and message revision');
}catch(error){results.textContent+='\nFAIL '+error.stack;window.fixtureFailure=error.message;}
