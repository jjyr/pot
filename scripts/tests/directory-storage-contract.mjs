import {mountApp} from '../../web/src/components/ui.js';
import {domainsFor} from '../../web/src/state/domains/index.mjs';
import {translate} from '../../web/src/locales/messages.mjs';
export async function directoryStorageContract(report=()=>{}){
  const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
  const wait=async test=>{for(let i=0;i<500;i++){if(test())return;await new Promise(done=>setTimeout(done,10));}throw Error('Directory storage UI timed out');};
  const root=document.createElement('div'),name='pot-directory-storage-'+crypto.randomUUID();document.body.append(root);
  let scope={space:'local',owner:'a'},failing=false,sends=0;
  const read=async path=>{
    if(path==='/api/v1/spaces')return {items:[{id:'local',name:'Synthetic',kind:'local',connected:true,control_id:'runtime'}]};
    if(path==='/api/v1/assistants'){if(failing)throw Object.assign(Error('Synthetic quota failed'),{storage:true,uiKey:'storage.failed'});return {items:[{id:'a',name:'Synthetic assistant',capabilities:{version:1,operations:['chat.read','chat.send']}}]};}
    return {items:[],policy:{}};
  };
  const client={production:true,chatCacheName:name,runtime:{runtime_id:'runtime',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},read,request:read,send:async()=>{sends++;return {};},changes:()=>()=>{},resources:{read,state:()=>({freshness:'fresh'}),subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}},subscribe(receive,status){queueMicrotask(()=>{status('connected');receive({kind:'reset',runtime_id:'runtime',owner_id:JSON.stringify(['local','a']),conversation_id:'chat',boot_id:'boot',cursor:'1',running:false,turn_id:null,messages:[{id:'m1',sequence:'1',version:1,role:'assistant',text:'Committed synthetic output',status:'complete'}],replies:[],history:{before:'1',after:'1'}});});return()=>{};}};
  const close=mountApp(root,client),domains=domainsFor(client);
  try{
    await wait(()=>root.querySelector('.composer-input')&&!root.querySelector('.composer-input').disabled&&root.querySelector('.connection-status.is-connected'));
    const input=root.querySelector('.composer-input'),send=root.querySelector('.send-button');input.value='synthetic draft';input.dispatchEvent(new Event('input',{bubbles:true}));await wait(()=>!send.disabled);
    const committed=await domains.directory.query();failing=true;try{await domains.directory.refresh();}catch(error){check(error.storage,'Directory refresh exposes the original storage failure');}
    await wait(()=>root.querySelector('.connection-status')?.textContent.includes(translate(client.uiLocale(),'storage.failed')));
    check(await domains.directory.query()===committed&&client.assistants[0].offline===false,'Directory storage failure retains committed content and known connectivity');
    root.querySelector('form.composer').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));
    check(send.disabled&&sends===0,'A directory storage error disables stale capabilities, including programmatic submit');
    for(const locale of ['zh-CN','en']){window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:locale}}));await wait(()=>root.querySelector('.connection-status').textContent.includes(translate(locale,'storage.failed')));check([...root.querySelectorAll('.connection-status button')].some(button=>button.textContent===translate(locale,'retry')),'Directory storage error and Retry follow '+locale+' after language changes');}
    const retry=[...root.querySelectorAll('.connection-status button')].find(button=>button.textContent===translate(client.uiLocale(),'retry'));check(retry,'Directory storage error offers explicit Retry without pretending the node is offline');
    failing=false;retry.click();await wait(()=>!send.disabled&&!root.querySelector('.connection-status').textContent.includes(translate(client.uiLocale(),'storage.failed')));
    check(root.querySelector('.composer-input').value==='synthetic draft','Directory Retry restores authority and preserves the draft');
  }finally{close();root.remove();await new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});}
  report('DONE directory storage UI contract');
}
