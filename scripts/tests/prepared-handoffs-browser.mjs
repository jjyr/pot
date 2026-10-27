import assert from 'node:assert/strict';

// Exercise changed content in existing native views, including scalar Text
// nodes. Identity witnesses come from connected candidate DOM, not CSS sizing.
export async function preparedRevisionTransfers(page,origin){
 await page.goto(origin+'/scripts/tests/fixtures/prepared-empty.html');
 const result=await page.evaluate(async()=>{
  const {createTaskConversation}=await import('/components/task-conversation.mjs');
  const {createTaskTranscript,appendTaskEvents}=await import('/state/task-transcript.mjs');
  document.body.replaceChildren();const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/styles.css';document.head.append(sheet);await new Promise(resolve=>sheet.onload=resolve);await document.fonts.ready;
  const root=document.createElement('div');root.className='page-content';root.style.cssText='height:650px;width:900px';document.body.append(root);
  const scope={space:'fixture',owner:'owner'},task={id:'handoff-task',assistant_id:'owner',phase:'running'},record={run_id:'run',request:{text:'Synthetic handoff'},info:task,capabilities:{version:1,operations:['task.read','task.events']}};
  const state=createTaskTranscript();appendTaskEvents(state,[{sequence:1,event:{turn_id:'run',update:{type:'tool_started',id:'call',name:'exec_command',args:{cmd:'Synthetic command'}}}},{sequence:2,event:{turn_id:'run',update:{type:'tool_state',id:'call',phase:'running'}}}]);
  const memory=new Map([[JSON.stringify([scope,task.id,'tool-1']),{open:true,raw:true}],[JSON.stringify([scope,task.id])+'work:tool-1',{open:true}]]);let approval=null;
  const ui=createTaskConversation({root,task,record,scope,locale:()=> 'en',client:{runtime:{},scope:()=>scope,request:async()=>({items:[]})},draft:'',onDraft(){},toolMemory:memory,approvalFor:()=>approval,onDecision(){}});
  const witnesses=new WeakMap();
  function observe(){for(const surface of document.querySelectorAll('[data-preparation-surface]')){const walk=document.createTreeWalker(surface,NodeFilter.SHOW_ELEMENT|NodeFilter.SHOW_TEXT);while(walk.nextNode())witnesses.set(walk.currentNode,walk.currentNode.textContent);}}
  const observer=new MutationObserver(observe);observer.observe(document.body,{subtree:true,childList:true,attributes:true,characterData:true});
  const frame=()=>new Promise(requestAnimationFrame),wait=async test=>{for(let i=0;i<600;i++){observe();if(test())return;await frame();}throw Error('Handoff did not settle');};
  const ready=()=>getComputedStyle(ui.history).visibility==='visible'&&!ui.transcript.hasAttribute('aria-busy');
  const witnessed=node=>!!node&&witnesses.get(node)===node.textContent;
  try{
   ui.update({record,state,fresh:true,approvals:[]});ui.start();await wait(()=>ready()&&ui.history.querySelector('.tool-raw pre'));
   appendTaskEvents(state,[{sequence:3,event:{turn_id:'run',update:{type:'tool_output',id:'call',text:'Prepared changed output',stream:'stdout'}}}]);ui.update({record,state,fresh:true,approvals:[]});await wait(()=>ready()&&ui.history.textContent.includes('Prepared changed output'));
   const raw=[...ui.history.querySelectorAll('.tool-raw pre')].find(node=>node.textContent.includes('Prepared changed output'));
   const rawPrepared=witnessed(raw)&&[...raw.childNodes].every(witnessed);
   approval={id:'approval',revision:1,fresh:true,request:{owner:'owner',task:task.id,call_id:'call',turn:'run',tool:'exec_command',args:{cmd:'First args'},machine_id:'fixture'}};
   appendTaskEvents(state,[{sequence:4,event:{turn_id:'run',update:{type:'tool_state',id:'call',phase:'waiting_approval'}}}]);ui.update({record,state,fresh:true,approvals:[approval]});await wait(()=>ready()&&ui.history.querySelector('.approval-card'));
   const first=ui.history.querySelector('.approval-card');approval={...approval,revision:2,request:{...approval.request,args:{cmd:'Changed approval args '.repeat(200)}}};ui.update({record,state,fresh:true,approvals:[approval]});
   const disabledBeforeCommit=[...first.querySelectorAll('button')].every(button=>button.disabled);
   await wait(()=>ready()&&ui.history.querySelector('.approval-card')!==first);const second=ui.history.querySelector('.approval-card');
   const revisedPrepared=witnessed(second)&&second.textContent.includes('Changed approval args');
   approval={...approval,fresh:false};ui.refreshApprovals();const staleDisabled=[...second.querySelectorAll('button')].every(button=>button.disabled);
   await wait(()=>ready()&&ui.history.querySelector('.approval-card')!==second);const stale=ui.history.querySelector('.approval-card');
   return {rawPrepared,disabledBeforeCommit,revisedPrepared,staleDisabled,stalePrepared:witnessed(stale),staleFinal:[...stale.querySelectorAll('button')].every(button=>button.disabled)};
  }finally{observer.disconnect();ui.dispose();}
 });
 for(const [name,passed]of Object.entries(result))assert(passed,name);
 return result;
}

export async function preparedResponsiveMedia(page,origin){
 await page.goto(origin+'/scripts/tests/fixtures/prepared-empty.html');
 const result=await page.evaluate(async()=>{
  const {createMessageWindow}=await import('/components/message-window.mjs');const {createReplyBubble}=await import('/components/reply.mjs');
  const {createPreparationSurface,settlePreparedTree,createPreparedRender,withPreparedRender,retirePreparedTree}=await import('/components/prepared-render.mjs');
  document.body.replaceChildren();const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/styles.css';document.head.append(sheet);await new Promise(resolve=>sheet.onload=resolve);await document.fonts.ready;
  const app=document.createElement('div');app.className='pot-app';app.style.cssText='display:block;height:650px';const scroller=document.createElement('div');scroller.className='history';scroller.style.cssText='height:600px;width:700px';const column=document.createElement('div');column.className='message-column';const host=document.createElement('div');host.className='message-window';column.append(host);scroller.append(column);app.append(scroller);document.body.append(app);
  const canvas=document.createElement('canvas');canvas.width=960;canvas.height=640;canvas.getContext('2d').fillRect(0,0,960,640);const blob=await new Promise(resolve=>canvas.toBlob(resolve)),url=URL.createObjectURL(blob);let acquired=0,released=0,ui;
  const resource={id:'responsive',name:'image.png',entry:'image.png',mime_type:'image/png',size:blob.size,files:[]},options={t:key=>key,acquireResource:()=>{acquired++;return {url:()=>url,release:()=>released++};},downloadResource:async()=>{}};
  const rows=[{id:'row',viewKey:'row',role:'assistant',text:'',phase:'complete',status:'complete',presentation:{resource,mode:'preview'}}];
  function render(message,old){if(!old){const row=document.createElement('article');row.className='message role-assistant';row.dataset.messageId=message.id;const body=document.createElement('div');body.className='message-body';row.append(body);old={row,reply:createReplyBubble(body)};}old.reply.update(message,options);return old;}
  const draw=()=>ui.setMessages(rows),wait=async test=>{for(let i=0;i<600;i++){if(test())return;await new Promise(requestAnimationFrame);}throw Error('Responsive media did not settle '+JSON.stringify({prepared:ui.prepared,width:scroller.clientWidth,height:scroller.clientHeight,resource:host.querySelector('pot-resource')?.dataset.state,surfaces:document.querySelectorAll('[data-preparation-surface]').length,acquired,released,image:host.querySelector('img')?.outerHTML,natural:host.querySelector('img')?.naturalWidth}));};
  ui=createMessageWindow({scroller,host,requestRender:draw,render,dispose:record=>retirePreparedTree(record.row)});
  try{draw();await wait(()=>ui.prepared&&host.querySelector('img')?.naturalWidth);const original=host.querySelector('pot-resource');scroller.style.width='340px';draw();await wait(()=>ui.prepared);
   const surface=createPreparationSurface(scroller,host),context=createPreparedRender(),fresh=withPreparedRender(context,'row',()=>render(rows[0]));surface.host.append(fresh.row);await settlePreparedTree(fresh.row,context).promise;
   const parity=fresh.row.getBoundingClientRect().height-host.querySelector('article').getBoundingClientRect().height,sameOriginal=host.querySelector('pot-resource')===original;surface.close();ui.close();await Promise.resolve();return {parity,sameOriginal,acquired,released};
  }finally{ui.close();URL.revokeObjectURL(url);}
 });
 assert(Math.abs(result.parity)<0.6,'Ready image reflow matches an independent current-width render');assert(result.sameOriginal);assert.equal(result.acquired,result.released);return result;
}
