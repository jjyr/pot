import {mountApp} from '../../web/src/components/ui.js';
import {domainsFor} from '../../web/src/state/domains/index.mjs';
import {translate} from '../../web/src/locales/messages.mjs';

// Hold the real resource repository's transport after an invalidation. The
// directory must publish its cached retained projection before fresh IO commits.
export async function conversationMenuRefreshContract(report=()=>{}){
  const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
  const wait=async test=>{for(let i=0;i<500;i++){if(test())return;await new Promise(done=>setTimeout(done,10));}throw Error('Menu refresh timed out');};
  const root=document.createElement('div'),name='pot-menu-refresh-'+crypto.randomUUID();document.body.append(root);
  let scope={space:'local',owner:'a'},held=false,release,receive,cursor=0,operations=['chat.read','chat.send','chat.cancel','chat.control','assistant.profile','assistant.usage'];
  const controls=[],reads=[];
  const request=async path=>{
    reads.push(path);
    if(path==='/api/v1/spaces')return {items:[{id:'local',name:'Synthetic',kind:'local',connected:true,control_id:'runtime'}]};
    if(path==='/api/v1/assistants'){
      if(held)await new Promise(done=>release=done);
      return {items:[{id:'a',name:'Synthetic assistant',capabilities:{version:1,operations}}]};
    }
    return {items:[],policy:{}};
  };
  const frame=(values={})=>({kind:'reset',runtime_id:'runtime',owner_id:JSON.stringify(['local','a']),conversation_id:'chat',boot_id:'boot',cursor:String(++cursor),running:true,turn_id:'synthetic-turn',turn:{paused:false},messages:[{id:'m1',sequence:'1',version:cursor,role:'assistant',text:'Synthetic output '+cursor,status:'complete'}],replies:[],history:{before:'1',after:'1'},...values});
  const client={production:true,chatCacheName:name,runtime:{runtime_id:'runtime',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},request,
    controlTurn:async(body,captured)=>{controls.push({body,captured});},changes:()=>()=>{},
    subscribe(onFrame,onStatus){receive=onFrame;queueMicrotask(()=>{onStatus('connected');receive(frame());});return()=>{};}};
  const close=mountApp(root,client),domains=domainsFor(client),dialog=()=>root.querySelector('dialog.preferences'),action=key=>dialog().querySelector('[data-view-key="'+key+'"]');
  try{
    await wait(()=>root.querySelector('.connection-status.is-connected')&&!root.querySelector('.send-button')?.disabled);
    root.querySelector('[data-view-key="conversation-actions"]').click();
    const keys=['assistant-settings','usage','turn-control'],nodes=keys.map(action),callbacks=nodes.map(node=>node.onclick),unavailable=()=>keys.slice(1).every(key=>action(key)?.getAttribute('aria-disabled')==='true')&&!action('assistant-settings')?.disabled&&!action('assistant-settings')?.hasAttribute('aria-disabled');
    nodes[1].focus();held=true;const refresh=domains.changed(['assistants'],{space:'local'});
    await wait(()=>release&&client.assistants[0].retained&&unavailable());
    check(keys.every((key,i)=>action(key)===nodes[i])&&document.activeElement===nodes[1]&&nodes.every(node=>node.getClientRects().length),'Held real repository revalidation keeps visible menu nodes and keyboard focus');
    check(client.assistants[0].capabilities.operations.join(',')==='chat.read','Retained directory never restores old execution capabilities');
    const before=reads.length;
    for(const [i,node]of nodes.entries()){
      if(i===0)continue; // Configuration remains discoverable without execution authority.
      node.click();callbacks[i]({currentTarget:node});
      // aria-disabled is presentation, not the execution guard. Even if it is
      // stripped, both the saved and the current handler must reject authority.
      node.removeAttribute('aria-disabled');node.click();callbacks[i]({currentTarget:node});
      node.setAttribute('aria-disabled','true');
    }
    check(!controls.length&&reads.length===before&&dialog().open,'Held revalidation fences node clicks and saved navigation/control callbacks independently of aria-disabled');
    for(const locale of ['zh-CN','en']){
      window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:locale}}));
      check(action('usage')===nodes[1]&&document.activeElement===nodes[1]&&nodes[1].textContent===translate(locale,'usage')&&unavailable(),'Held menu keeps focus and accessible unavailable actions when switching to '+locale);
    }
    const currentControl=nodes[2].onclick;
    scope={...scope,owner:'other'};nodes[2].removeAttribute('aria-disabled');currentControl({currentTarget:nodes[2]});callbacks[0]({currentTarget:nodes[0]});scope={...scope,owner:'a'};
    check(!controls.length&&dialog().open,'Held revalidation cannot redirect a captured control to another scope');
    nodes[2].setAttribute('aria-disabled','true');
    held=false;release();await refresh;
    await wait(()=>!client.assistants[0].retained&&keys.every(key=>action(key)&&!action(key).disabled&&!action(key).hasAttribute('aria-disabled')));
    check(keys.every((key,i)=>action(key)===nodes[i])&&document.activeElement===nodes[1],'Fresh capabilities restore the same menu nodes and keyboard focus');
    const closed=new Promise(done=>dialog().addEventListener('close',done,{once:true}));nodes[2].click();await closed;await wait(()=>controls.length===1);
    check(controls[0].body.action==='pause'&&controls[0].captured.owner==='a','Fresh menu action executes with its current authority and original destination');
    // A turn change during the next held refresh must retire the presentation,
    // and a fresh response that really removes capabilities must not restore it.
    await wait(()=>!dialog().open);root.querySelector('[data-view-key="conversation-actions"]').click();
    const old=action('turn-control'),callback=old.onclick;old.focus();held=true;release=undefined;
    const revoked=domains.changed(['assistants'],{space:'local'});
    await wait(()=>release&&action('turn-control')?.getAttribute('aria-disabled')==='true');
    const heldCallback=old.onclick,next=frame({turn_id:'replacement-turn'});receive(next);
    await wait(()=>root.textContent.includes('Synthetic output '+next.cursor)&&!action('turn-control'));
    old.removeAttribute('aria-disabled');old.click();callback({currentTarget:old});heldCallback({currentTarget:old});
    check(controls.length===1&&!old.isConnected&&dialog().open,'A replacement turn during held revalidation retires the old node and both saved callbacks');
    operations=['chat.read'];held=false;release();await revoked;
    await wait(()=>!client.assistants[0].retained&&keys.every(key=>!action(key)));
    old.click();callback({currentTarget:old});heldCallback({currentTarget:old});
    check(controls.length===1&&dialog().open,'Fresh capability removal keeps old nodes and callbacks unable to execute');
  }finally{
    held=false;release?.();close();root.remove();await new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});
  }
  report('DONE conversation menu retained refresh contract');
}
