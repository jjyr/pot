import {mountApp} from '../../web/src/components/ui.js';
const root=document.querySelector('#app'),errors=[],checks=[];
window.addEventListener('error',event=>errors.push(event.message));window.addEventListener('unhandledrejection',event=>errors.push(String(event.reason?.stack||event.reason)));
localStorage.setItem('pot-system-notifications-browser-v1','false');
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const wait=async(test)=>{for(let i=0;i<750;i++){if(test())return;await pause(20);}throw Error('Timed out: '+test);};
const check=(condition,message)=>{if(!condition)throw Error(message);checks.push(message);};
const click=text=>{const button=[...root.querySelectorAll('button')].find(item=>item.textContent===text);if(!button)throw Error('Missing button '+text);button.click();};
const chinese=new URLSearchParams(location.search).get('locale')==='zh-CN';
let scope={space:'local',owner:''},assistants=[],links=[],created=0,bootstraps=0,defaultProvider='fixture',failDirectory=true,remoteDefault='codex',remoteKind='codex',managed=[];
let remoteAssistants=[{address:{node_id:'codex-node',kind:'assistant',agent_id:'codex'},name:'Codex',adapter:'codex'}];
const frame=(target=scope)=>({kind:'reset',runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify(['local',target.owner]),conversation_id:target.owner,cursor:'1',replies:[],running:false,turn_id:null,messages:[{id:'welcome',sequence:'1',role:'assistant',text:'Fixture conversation',status:'complete'}],history:{before:'1',after:'1',has_more:false,has_newer:false}});
const client={chatCacheName:'pot-assistant-start-'+crypto.randomUUID(),production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
 snapshot:async()=>frame(),history:async()=>frame(),
 subscribe(receive,status){const target={...scope};const timer=setTimeout(()=>{status('connected');receive(frame(target));},10);return()=>clearTimeout(timer);},
 async request(path,options={}){const url=new URL(path,location.href);
  if(url.pathname==='/api/v1/snapshot')return frame();
  if(url.pathname==='/api/v1/spaces')return {active_space_id:'local',items:[{id:'local',kind:'local',name:'Fixture',control_id:'fixture',connected:true}]};
  if(url.pathname==='/api/v1/assistants/bootstrap'){bootstraps++;return {};}
  if(url.pathname==='/api/v1/assistants'){if(options.method==='POST'){created++;const assistant={id:'local-'+created,name:'Cup'};assistants.push(assistant);return assistant;}return {items:assistants};}
  if(url.pathname==='/api/v1/providers')return {default_provider_id:defaultProvider,profiles:[],provider_types:[{id:'custom',name:'Custom API',available:true}],directory:[],connections:[]};
  if(url.pathname==='/api/v1/agent-links/assistants'){
   const value=options.body;
   if(value.action==='catalog')return {models:[{id:'model-one',name:'Gemini fixture'},{id:'model-two',name:'Claude fixture'}],workspaces:[{id:'template',name:'Isolated workspace'}],assistants:managed};
   const item={...value,revision:1};managed.push(item);remoteAssistants=[{address:{node_id:'codex-node',kind:'assistant',agent_id:item.id},name:item.name,adapter:'antigravity'}];return {assistant:item};
  }
  if(url.pathname==='/api/v1/agent-links'){
   if(options.body?.action==='preview'){await pause(200);return {peer:{node_id:'codex-node',kind:remoteKind,name:remoteKind==='antigravity'?'Antigravity':'Codex'},endpoint:'tcp://127.0.0.1:8793',fingerprint:'SHA256:fixture'};}
   if(options.body?.action==='connect'){const link={id:'linked',peer:{node_id:'codex-node',kind:remoteKind,name:remoteKind==='antigravity'?'Antigravity':'Codex'},status:'connected',directory:{default_target:{kind:'assistant',agent_id:remoteDefault},targets:[...(remoteKind==='antigravity'?[{address:{node_id:'codex-node',kind:'node',agent_id:'codex-node'},name:'Antigravity',adapter:'antigravity',capabilities:{version:1,scope:'caller',operations:['assistant.management','task.submit']}}]:[]),...remoteAssistants.map(item=>({...item,capabilities:item.capabilities||{version:1,scope:'peer',operations:['chat.read','chat.send','chat.receipt','chat.subscribe']}}))]}};links=[link];return link;}
   if(failDirectory)throw Error('Synthetic directory unavailable');
   if(links[0])links[0].directory={default_target:{kind:'assistant',agent_id:remoteDefault},targets:[...(remoteKind==='antigravity'?[{address:{node_id:'codex-node',kind:'node',agent_id:'codex-node'},name:'Antigravity',adapter:'antigravity',capabilities:{version:1,scope:'caller',operations:['assistant.management','task.submit']}}]:[]),...remoteAssistants.map(item=>({...item,capabilities:item.capabilities||{version:1,scope:'peer',operations:['chat.read','chat.send','chat.receipt','chat.subscribe']}}))]};
   return {items:links};
  }
  return {items:[],pending:[],policy:{}};
 }};
window.assistantStartFixture={client,get created(){return created},get errors(){return errors}};
mountApp(root,client);
async function run(){
 await wait(()=>root.querySelector('.empty-chat button'));
 check(!root.querySelector('.assistant-start-option'),'Failed directory offers retry rather than setup');
 failDirectory=false;root.querySelector('.empty-chat button').click();
 await wait(()=>root.querySelectorAll('.assistant-start-option').length===4);
 check(created===0&&bootstraps===0,'First load creates no assistant and does not bootstrap');
 check([...root.querySelectorAll('.assistant-start-option h2')].map(item=>item.textContent).join('|')===(chinese?'使用 API 创建助手|连接另一个 Pot|连接 Codex|连接 Antigravity':'Create an assistant with API|Connect another Pot|Connect Codex|Connect Antigravity'),'API, Pot, Codex order');
 check(root.querySelector('.composer-area').hidden,'Composer hidden on add page');
 if(!matchMedia('(max-width:719px)').matches)check(root.querySelector('.assistant-directory').getBoundingClientRect().width>=220,'Desktop directory remains visible');
 click(chinese?'连接 Codex':'Connect Codex');await wait(()=>root.querySelector('.agent-connection-setup'));
 check(root.textContent.includes('pot-setup'),'Codex-specific guide');
 click(chinese?'取消':'Cancel');await wait(()=>root.querySelector('.assistant-start-option'));
 click(chinese?'连接 Pot':'Connect Pot');await wait(()=>root.querySelector('.agent-connection-setup'));
 check(!root.querySelector('.connection-intro').textContent.includes('pot-setup'),'Pot-specific guide');
 click(chinese?'取消':'Cancel');await wait(()=>root.querySelector('.assistant-start-option'));
 click(chinese?'创建助手':'Create assistant');await wait(()=>scope.owner==='local-1'&&!root.querySelector('.history').hidden);await pause(100);
 check(created===1,'Explicit API action creates exactly one assistant');
 const input=root.querySelector('#pot-composer');input.value='Draft retained\nSecond line';input.dispatchEvent(new Event('input',{bubbles:true}));
 if(matchMedia('(max-width:719px)').matches){root.querySelector('.narrow-navigation').click();await pause(50);}
 await wait(()=>root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]:not(:disabled)'));root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]').click();await wait(()=>root.querySelector('.assistant-start-option'));
 check(created===1,'Plus opens shared page without creating an assistant');
 root.querySelector('.titlebar button').click();
 await wait(()=>!root.querySelector('.history').hidden);
 check(input.value==='Draft retained\nSecond line','Returning preserves the original conversation draft');
 if(matchMedia('(max-width:719px)').matches){root.querySelector('.narrow-navigation').click();await pause(50);}
 await wait(()=>root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]:not(:disabled)'));root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]').click();await wait(()=>root.querySelector('.assistant-start-option'));
 click(chinese?'连接 Codex':'Connect Codex');await wait(()=>root.querySelector('.agent-connection-setup'));
 const address=root.querySelector('.agent-connection-setup input');address.value='tcp://fixture#invitation';address.dispatchEvent(new Event('input',{bubbles:true}));
 await wait(()=>scope.owner==='agent-link:linked:codex'&&root.dataset.route==='chat');
 check(created===1,'Connecting Codex opens remote assistant without creating a local assistant');
 async function openConnection(){
  if(matchMedia('(max-width:719px)').matches&&root.dataset.route==='chat'){root.querySelector('.narrow-navigation').click();await pause(50);}
  await wait(()=>root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]:not(:disabled)'));root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]').click();await wait(()=>root.querySelector('.assistant-start-option'));
  click(chinese?'连接 Codex':'Connect Codex');await wait(()=>root.querySelector('.agent-connection-setup'));
  const field=root.querySelector('.agent-connection-setup input');field.value='tcp://fixture#next';field.dispatchEvent(new Event('input',{bubbles:true}));
 }
 remoteDefault='';remoteAssistants=['one','two'].map(id=>({address:{node_id:'codex-node',kind:'assistant',agent_id:id},name:'Receiver '+id,adapter:'codex'}));
 await openConnection();await wait(()=>[...root.querySelectorAll('button')].some(item=>item.textContent==='Receiver two'));
 check(root.dataset.route==='add-assistant','Multiple remote assistants without a default require a choice');click('Receiver two');await wait(()=>scope.owner==='agent-link:linked:two'&&root.dataset.route==='chat');
 remoteAssistants=[];await openConnection();await wait(()=>[...root.querySelectorAll('h1')].some(item=>item.textContent===(chinese?'已连接，等待助手':'Connected, waiting for assistants')));
 check(root.dataset.route==='add-assistant'&&created===1,'Empty remote directory stays connected without creating a local assistant');
 const alternate=chinese?'en':'zh-CN';window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:alternate}}));
 await wait(()=>[...root.querySelectorAll('h1')].some(item=>item.textContent===(chinese?'Connected, waiting for assistants':'已连接，等待助手')));
 window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:chinese?'zh-CN':'en'}}));
 await wait(()=>[...root.querySelectorAll('h1')].some(item=>item.textContent===(chinese?'已连接，等待助手':'Connected, waiting for assistants')));
 check(!root.querySelector('.assistant-start-options'),'Locale changes retain the connected directory view');

 remoteAssistants=[{address:{node_id:'codex-node',kind:'assistant',agent_id:'ready'},name:'Ready',adapter:'codex'}];click(chinese?'刷新助手':'Refresh assistants');await wait(()=>scope.owner==='agent-link:linked:ready'&&root.dataset.route==='chat');
 check(created===1,'Refreshing an empty directory opens the newly available assistant');

 remoteKind='antigravity';remoteAssistants=[];remoteDefault='';
 if(matchMedia('(max-width:719px)').matches){root.querySelector('.narrow-navigation').click();await pause(50);}
 await wait(()=>root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]:not(:disabled)'));root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]').click();await wait(()=>root.querySelector('.assistant-start-option'));
 click(chinese?'连接 Antigravity':'Connect Antigravity');await wait(()=>root.querySelector('.agent-connection-setup'));
 const nativeAddress=root.querySelector('.agent-connection-setup input');nativeAddress.value='tcp://fixture#antigravity';nativeAddress.dispatchEvent(new Event('input',{bubbles:true}));
 await wait(()=>root.querySelector('select[aria-label="'+(chinese?'Antigravity 模型':'Antigravity model')+'"]'));
 const model=root.querySelector('select[aria-label="'+(chinese?'Antigravity 模型':'Antigravity model')+'"]');model.value='model-two';model.dispatchEvent(new Event('input',{bubbles:true}));
 const name=root.querySelector('input[aria-label="'+(chinese?'助手名称':'Assistant name')+'"]');name.value='Native fixture';name.dispatchEvent(new Event('input',{bubbles:true}));
 window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:chinese?'en':'zh-CN'}}));
 await wait(()=>root.querySelector('select[aria-label="'+(chinese?'Antigravity model':'Antigravity 模型')+'"]'));
 check(model.value==='model-two'&&name.value==='Native fixture','Locale switch preserves native assistant draft');
 window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-go-preview-locale',value:chinese?'zh-CN':'en'}}));
 if(new URLSearchParams(location.search).get('capture')==='native'){window.nativeAssistantCaptureReady=true;await new Promise(resolve=>{window.nativeAssistantCaptureContinue=resolve});}
 root.querySelector('.editor-actions .primary').click();await wait(()=>managed.length===1&&scope.owner==='agent-link:linked:'+managed[0].id&&root.dataset.route==='chat');
 check(managed[0].model==='model-two'&&managed[0].workspace==='template','Native creation submits exact model and authorized workspace');
 check(errors.length===0,errors.join('\n'));
 window.assistantStartResults={passed:true,checks,errors};
 if(new URLSearchParams(location.search).get('capture')==='1'){if(matchMedia('(max-width:719px)').matches){root.querySelector('.narrow-navigation').click();await pause(50);}await wait(()=>root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]:not(:disabled)'));root.querySelector('.directory-heading button[aria-label="'+(chinese?'添加助手':'Add assistant')+'"]').click();}
}
run().catch(error=>window.assistantStartResults={passed:false,checks,errors:[...errors,String(error.stack||error)],view:root.textContent.slice(0,2000)});
