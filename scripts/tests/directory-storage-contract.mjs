import {mountApp} from '../../web/src/components/ui.js';
import {domainsFor} from '../../web/src/state/domains/index.mjs';
import {translate} from '../../web/src/locales/messages.mjs';
export async function directoryStorageContract(report=()=>{}){
  const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
  const wait=async test=>{for(let i=0;i<500;i++){if(await test())return;await new Promise(done=>setTimeout(done,10));}throw Error('Directory storage UI timed out');};
  const root=document.createElement('div'),name='pot-directory-storage-'+crypto.randomUUID();document.body.append(root);
  let scope={space:'local',owner:'a'},failing=false;const deliveries=[];
  const read=async path=>{
    if(path==='/api/v1/spaces')return {items:[{id:'local',name:'Synthetic',kind:'local',connected:true,control_id:'runtime'}]};
    if(path==='/api/v1/assistants'){if(failing)throw Object.assign(Error('Synthetic quota failed'),{storage:true,uiKey:'storage.failed'});return {items:[{id:'a',name:'Synthetic assistant',capabilities:{version:1,operations:['chat.read','chat.send']}}]};}
    return {items:[],policy:{}};
  };
  const snapshot=()=>({kind:'reset',runtime_id:'runtime',owner_id:JSON.stringify(['local','a']),conversation_id:'chat',boot_id:'boot',cursor:'1',running:false,turn_id:null,messages:[{id:'m1',sequence:'1',version:1,role:'assistant',text:'Committed synthetic output',status:'complete'}],replies:[],history:{before:'1',after:'1'}});
  const client={production:true,chatCacheName:name,connectionGeneration:0,directoryGeneration:0,get operationsReady(){return this.directoryGeneration===this.connectionGeneration;},operationScopeReady:()=>client.operationsReady,runtime:{runtime_id:'runtime',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},read,request:read,snapshot:async()=>snapshot(),send:async(request,captured)=>{deliveries.push({request:structuredClone(request),scope:structuredClone({...captured,signal:undefined})});return {message_id:'accepted-synthetic',duplicate:false};},changes:()=>()=>{},resources:{read,state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}},subscribe(receive,status){queueMicrotask(()=>{status('connected');receive({kind:'reset',runtime_id:'runtime',owner_id:JSON.stringify(['local','a']),conversation_id:'chat',boot_id:'boot',cursor:'1',running:false,turn_id:null,messages:[{id:'m1',sequence:'1',version:1,role:'assistant',text:'Committed synthetic output',status:'complete'}],replies:[],history:{before:'1',after:'1'}});});return()=>{};}};
  const close=mountApp(root,client),domains=domainsFor(client);let inspection;
  const committed=async()=>{inspection ||= await new Promise((resolve,reject)=>{const op=indexedDB.open(name);op.onsuccess=()=>resolve(op.result);op.onerror=()=>reject(op.error);});return new Promise((resolve,reject)=>{const tx=inspection.transaction(['chatOutbox','composerDrafts'],'readonly'),outbox=tx.objectStore('chatOutbox').getAll(),drafts=tx.objectStore('composerDrafts').getAll();tx.oncomplete=()=>resolve({outbox:outbox.result,drafts:drafts.result});tx.onabort=()=>reject(tx.error);});};
  try{
    await wait(()=>root.querySelector('.composer-input')&&!root.querySelector('.composer-input').disabled&&root.querySelector('.connection-status.is-connected'));
    const input=root.querySelector('.composer-input'),send=root.querySelector('.send-button');input.value='synthetic draft';input.dispatchEvent(new Event('input',{bubbles:true}));await wait(()=>!send.disabled);
    const directory=await domains.directory.query();failing=true;try{await domains.directory.refresh();}catch(error){check(error.storage,'Directory refresh exposes the original storage failure');}
    await wait(()=>root.querySelector('.connection-status')?.textContent.includes(translate(client.uiLocale(),'storage.failed')));
    check(await domains.directory.query()===directory&&client.assistants[0].offline===false,'Directory storage failure retains committed content and known connectivity');
    // This explicit submit may commit locally with a known immutable target.
    // Inject a newer edit after enqueue starts but before its real IDB commit.
    let enqueueComplete=false;const add=IDBObjectStore.prototype.add;
    IDBObjectStore.prototype.add=function(value,...args){
      const op=add.call(this,value,...args);
      if(this.name==='chatOutbox'){
        check(input.value==='synthetic draft','Submitted draft remains until its outbox transaction commits');
        input.value='new draft B';input.dispatchEvent(new Event('input',{bubbles:true}));
        this.transaction.addEventListener('complete',()=>{enqueueComplete=true;});
      }return op;
    };
    try{root.querySelector('form.composer').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));await wait(async()=>enqueueComplete&&(await committed()).outbox.length===1);}
    finally{IDBObjectStore.prototype.add=add;}
    const first=(await committed()).outbox[0],target={host:'runtime',control:'runtime',space:'local',owner:'a',conversation:'chat'};
    check(first.request.text==='synthetic draft'&&typeof first.request.request_id==='string'&&first.request.request_id.length>0&&Object.entries(target).every(([key,value])=>first.target[key]===value)&&first.target.key===JSON.stringify(Object.values(target)),'Directory fault commits exactly one explicit outbox request with the full original target');
    await wait(async()=>(await committed()).drafts.some(draft=>draft.text==='new draft B'));
    check(input.value==='new draft B'&&!client.operationsReady&&deliveries.length===0,'Directory storage fault permits local commitment, preserves new input and forbids network delivery');
    for(const locale of ['zh-CN','en']){window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:locale}}));await wait(()=>root.querySelector('.connection-status').textContent.includes(translate(locale,'storage.failed')));check([...root.querySelectorAll('.connection-status button')].some(button=>button.textContent===translate(locale,'retry')),'Directory storage error and Retry follow '+locale+' after language changes');}
    const retry=[...root.querySelectorAll('.connection-status button')].find(button=>button.textContent===translate(client.uiLocale(),'retry'));check(retry,'Directory storage error offers explicit Retry without pretending the node is offline');
    failing=false;retry.click();await wait(()=>!root.querySelector('.connection-status').textContent.includes(translate(client.uiLocale(),'storage.failed')));
    await wait(async()=>(await committed()).outbox[0]?.state==='accepted');const recovered=await committed();
    check(recovered.outbox.length===1&&recovered.outbox[0].sequence===first.sequence&&recovered.outbox[0].request.request_id===first.request.request_id&&deliveries.length===1&&deliveries[0].scope.owner==='a'&&deliveries[0].scope.space==='local'&&deliveries[0].request.request_id===first.request.request_id,'Directory Retry delivers the original committed request once, with no duplicate enqueue or other assistant');
    check(root.querySelector('.composer-input').value==='new draft B'&&recovered.drafts.some(draft=>draft.text==='new draft B'),'Directory Retry restores authority and preserves the newer unsubmitted draft');
  }catch(error){report('DIAGNOSTIC '+JSON.stringify({directoryGeneration:client.directoryGeneration,connectionGeneration:client.connectionGeneration,deliveries,committed:await committed()}));throw error;}finally{close();inspection?.close();root.remove();await new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});}
  report('DONE directory storage UI contract');
}
