import {mountApp} from '../../web/src/components/ui.js';
import {domainsFor} from '../../web/src/state/domains/index.mjs';
import {translate} from '../../web/src/locales/messages.mjs';
import {conversationMenuRefreshContract} from './conversation-menu-refresh-contract.mjs';

// Real shell + domain + isolated IndexedDB. Saved callbacks deliberately bypass
// DOM disabled/removal so these assertions exercise execution-time authority.
export async function conversationActionsContract(report=()=>{}){
  const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
  const wait=async test=>{for(let i=0;i<500;i++){if(test())return;await new Promise(done=>setTimeout(done,10));}const history=root.querySelector('.history:not([data-preparation-surface])'),host=root.querySelector('.message-window');throw Error('Conversation actions timed out '+JSON.stringify({root:root.getBoundingClientRect().toJSON(),history:history&&{width:history.clientWidth,height:history.clientHeight,hidden:history.hidden,busy:history.getAttribute('aria-busy')},host:host&&{width:host.clientWidth,busy:host.getAttribute('aria-busy')},text:root.textContent.slice(-300)}));};
  const root=document.createElement('div'),name='pot-conversation-actions-'+crypto.randomUUID();document.body.append(root);
  const operations=['chat.read','chat.send','chat.cancel','chat.control','assistant.profile','assistant.usage'];
  let scope={space:'local',owner:'a'},failing=false,connected=true,freshness='fresh',capabilities=operations,conversationBoot='boot',cursor=0,receive,status;
  const controls=[],reads=[],configurationReads=[];let cancels=0;
  const read=async(path,options={})=>{
    reads.push(path);if(options.configuration)configurationReads.push({path,...options});
    if(path==='/api/v1/spaces')return {items:[{id:'local',name:'Synthetic',kind:'local',connected,control_id:'runtime'}]};
    if(path==='/api/v1/assistants'){
      if(failing)throw Object.assign(Error('Synthetic storage failure'),{storage:true,uiKey:'storage.failed'});
      return {items:['a','b'].map(id=>({id,name:'Synthetic '+id,capabilities:{version:1,operations:capabilities}}))};
    }
    return {items:[],policy:{}};
  };
  const frame=(values={})=>({kind:'reset',runtime_id:'runtime',owner_id:JSON.stringify(['local',scope.owner]),conversation_id:'chat',boot_id:conversationBoot,cursor:String(++cursor),running:true,turn_id:'synthetic-turn',turn:{paused:false},messages:[{id:'m1',sequence:'1',version:cursor,role:'assistant',text:'Committed synthetic output '+cursor,status:'complete'}],replies:[],history:{before:'1',after:'1'},...values});
  const client={production:true,chatCacheName:name,connectionGeneration:0,directoryGeneration:0,get operationsReady(){return this.directoryGeneration===this.connectionGeneration;},operationScopeReady:()=>client.operationsReady,runtime:{runtime_id:'runtime',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},read,request:read,
    controlTurn:async(body,captured)=>{controls.push({body,captured});},cancel:async()=>{cancels++;},changes:()=>()=>{},
    resources:{read,state:()=>({freshness}),invalidate:async()=>{},subscribe:()=>()=>{},subscribeInvalidation:()=>()=>{}},
    subscribe(onFrame,onStatus){receive=onFrame;status=onStatus;queueMicrotask(()=>{status('connected');receive(frame());});return()=>{};}};
  const close=mountApp(root,client),domains=domainsFor(client);
  const dialog=()=>root.querySelector('dialog.preferences'),action=key=>dialog().querySelector('[data-view-key="'+key+'"]');
  const open=()=>{if(!dialog().open)root.querySelector('[data-view-key="conversation-actions"]').click();};
  const saved=key=>{const node=action(key);check(node&&!node.disabled,'Current '+key+' action is available');const callback=node.onclick;return(clickNode=false)=>{if(clickNode)node.click();callback({currentTarget:node});};};
  const publish=async values=>{const next=frame(values);receive(next);await wait(()=>root.textContent.includes('Committed synthetic output '+next.cursor));};
  try{
    await wait(()=>root.querySelector('.connection-status.is-connected')&&!root.querySelector('.send-button')?.disabled);
    for(const paused of [false,true]){
      await publish({turn:{paused}});open();
      const oldControl=saved('turn-control'),oldSettings=saved('assistant-settings'),oldUsage=saved('usage'),before=controls.length;
      const committed=await domains.directory.query();failing=true;
      try{await domains.directory.refresh();throw Error('Expected storage failure');}catch(error){check(error.storage,'Directory refresh rejects the injected storage failure');}
      await wait(()=>root.querySelector('.connection-status').textContent.includes(translate(client.uiLocale(),'storage.failed')));
      oldControl(true);check(controls.length===before,'Old '+(paused?'Resume':'Pause')+' nodes and callbacks cannot send after storage authority is lost');
      check(!action('turn-control')&&action('assistant-settings')&&!action('assistant-settings').disabled&&!action('usage')&&action('settings')&&!action('settings').disabled,'Open menu revokes execution and retains discoverable assistant and global Settings');
      const readCount=reads.length;oldUsage(true);root.querySelector('.send-button').dispatchEvent(new MouseEvent('click',{bubbles:true}));
      check(controls.length===before&&cancels===0&&reads.length===readCount&&dialog().open,'Old '+(paused?'Resume':'Pause')+' nodes/callbacks and Usage capability navigation cannot execute during storage failure');
      check(await domains.directory.query()===committed&&client.assistants.every(item=>!item.offline)&&root.textContent.includes('Committed synthetic output'),'Storage failure keeps committed content and known online state');
      for(const locale of ['zh-CN','en']){
        window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:locale}}));
        check(root.querySelector('.connection-status').textContent.includes(translate(locale,'storage.failed'))&&[...root.querySelectorAll('.connection-status button')].some(node=>node.textContent===translate(locale,'retry')),'Storage failure and Retry remain localized in '+locale);
      }
      oldSettings();await wait(()=>configurationReads.some(item=>item.owner==='a'&&item.space==='local'&&item.control_id==='runtime'&&item.configurationBoot==='boot'));
      check(controls.length===before&&cancels===0&&!client.operationsReady,'Read-only settings navigation keeps its original identity without restoring command authority');
      root.querySelector('.rail-button[aria-label="'+translate(client.uiLocale(),'chat')+'"]').click();await wait(()=>root.querySelector('.history:not([data-preparation-surface])')?.hidden===false);open();
      failing=false;[...root.querySelectorAll('.connection-status button')].find(node=>node.textContent===translate(client.uiLocale(),'retry')).click();
      await wait(()=>action('turn-control')&&!action('turn-control').disabled&&!root.querySelector('.connection-status').textContent.includes(translate(client.uiLocale(),'storage.failed')));
      const closed=new Promise(done=>dialog().addEventListener('close',done,{once:true}));action('turn-control').click();await closed;await wait(()=>controls.length===before+1);
      check(controls.at(-1).body.action===(paused?'resume':'pause')&&controls.at(-1).body.user_message_id==='synthetic-turn'&&controls.at(-1).captured.owner==='a','Explicit Retry restores current '+(paused?'Resume':'Pause')+' control with its original scope');
    }
    open();const oldPause=saved('turn-control'),beforeState=controls.length;
    await publish({turn:{paused:false}});oldPause();check(controls.length===beforeState,'Old Resume callback rejects a changed pause state');
    const oldTurn=saved('turn-control');await publish({turn_id:'next-turn'});oldTurn();check(controls.length===beforeState,'Old turn callback cannot act on a replacement turn');
    const completed=saved('turn-control');await publish({running:false,turn_id:null});completed();check(controls.length===beforeState,'Completed turn revokes its old callback');check(!action('turn-control'),'Completed turn removes its visible control');
    await publish();const oldScope=saved('turn-control'),oldSettings=saved('assistant-settings'),oldUsage=saved('usage');
    scope={...scope,owner:'b'};oldScope();oldSettings();oldUsage();check(controls.length===beforeState&&dialog().open,'Captured callbacks reject a different current scope before rendering');scope={...scope,owner:'a'};
    const oldSelection=saved('turn-control'),oldSelectionSettings=saved('assistant-settings'),oldSelectionUsage=saved('usage');
    for(const owner of ['b','a']){
      [...root.querySelectorAll('.assistant-row')].find(node=>node.dataset.viewKey===JSON.stringify(['local',owner])).click();
      await wait(()=>scope.owner===owner&&root.querySelector('.connection-status.is-connected')&&action('turn-control')&&!action('turn-control').disabled);
      oldSelection();oldSelectionSettings();oldSelectionUsage();
      check(controls.length===beforeState&&dialog().open,'Old menu callbacks stay retired after selecting '+owner+' with the same turn ID');
    }
    const oldBoot=saved('turn-control');conversationBoot='new-boot';await publish();oldBoot();check(controls.length===beforeState,'Old control rejects a changed conversation boot');
    const oldConnection=saved('turn-control');status('offline');oldConnection();check(controls.length===beforeState&&action('turn-control').disabled,'Disconnected control is disabled and its saved callback is fenced');
    status('connected');await wait(()=>action('turn-control')&&!action('turn-control').disabled);oldConnection();check(controls.length===beforeState,'Reconnect retires callbacks from the prior subscription epoch');
    const oldCapabilities=saved('turn-control'),oldProfile=saved('assistant-settings'),oldUsageCapability=saved('usage');capabilities=['chat.read'];await domains.directory.refresh();await wait(()=>!action('turn-control')&&!action('assistant-settings')&&!action('usage'));
    oldCapabilities();oldProfile();oldUsageCapability();check(controls.length===beforeState&&dialog().open,'Capability removal refreshes the open menu and fences all capability callbacks');
    capabilities=operations;await domains.directory.refresh();await wait(()=>action('turn-control'));const retained=saved('turn-control'),retainedNode=action('turn-control');retainedNode.focus();freshness='retained';await domains.directory.refresh();await wait(()=>action('turn-control')?.getAttribute('aria-disabled')==='true');retained(true);check(controls.length===beforeState&&action('turn-control')===retainedNode&&document.activeElement===retainedNode&&action('settings')&&!action('settings').disabled,'Retained directory projection keeps the focused node, revokes execution and preserves Settings');
    connected=false;await domains.directory.refresh();await wait(()=>!action('turn-control')&&action('assistant-settings')&&!action('assistant-settings').disabled&&!action('usage'));retainedNode.removeAttribute('aria-disabled');retained(true);check(controls.length===beforeState&&action('settings')&&!retainedNode.isConnected,'An offline retained destination keeps Settings discoverable and removes execution actions, fencing its old node and callback');
    connected=true;freshness='fresh';await domains.directory.refresh();status('connected');await wait(()=>action('turn-control')&&!action('turn-control').disabled);
    const disposedControl=saved('turn-control');failing=true;await domains.directory.refresh().catch(()=>{});await wait(()=>!action('turn-control'));
    action('settings').click();await wait(()=>root.querySelector('.rail-button.is-selected')?.getAttribute('aria-label')===translate(client.uiLocale(),'settings'));
    check(!dialog().open,'Settings navigation remains usable during directory storage failure');
    close();disposedControl();check(controls.length===beforeState,'Disposed shell callbacks cannot send a control request');
  }finally{
    close();root.remove();await new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});
  }
  await conversationMenuRefreshContract(report);
  report('DONE conversation actions authority contract');
}
