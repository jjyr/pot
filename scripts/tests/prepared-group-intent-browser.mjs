import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
const source=await sourceServer();let browser;
try{
 browser=await launchBrowser();const page=await browser.newPage(),errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 if(process.env.POT_GROUP_BASELINE){const body=await readFile(process.env.POT_GROUP_BASELINE,'utf8');await page.route('**/components/work-record-window.mjs',route=>route.fulfill({contentType:'text/javascript',body}));}
 await page.goto(source.origin+'/scripts/tests/fixtures/prepared-empty.html');
 const results=await page.evaluate(async baseline=>{
  const {createWorkRecordWindow}=await import('/components/work-record-window.mjs');
  const {createWorkRecordProjection}=await import('/state/work-records.mjs');
  const {createToolCall}=await import('/components/tool-call.mjs');
  const {currentPreparedRender}=await import('/components/prepared-render.mjs');
  const actor={node_id:'fixture',kind:'assistant',agent_id:'from'},to={...actor,agent_id:'to'};
  const messages=[{id:'mail',viewKey:'mail',role:'tool',tool:{id:'mail',turn_id:'turn',name:'send_message',phase:'completed',args:{to,text:'Synthetic group member'},result:{accepted:true,message_id:'mail'}}}];
  const group=createWorkRecordProjection()(messages,{actor})[0],results=[];
  await document.fonts.ready;
  for(const [initial,latest,rapid]of [[false,true,false],[true,false,false],[false,true,true]]){
   const scroller=document.createElement('div'),host=document.createElement('div');scroller.style.cssText='overflow:auto;width:700px;height:400px';scroller.className='history';host.className='message-window';scroller.append(host);document.body.append(scroller);
   const memory=new Map([['scope'+group.viewKey,{open:initial}]]),trace=[],measurements=new WeakMap();let changed=false,commits=0,automatic=0,view,measured,preparedMembers=[],previousMembers=[];
   const settings={beforeCommit(){const candidate=document.querySelector('[data-preparation-surface] .work-record-group');preparedMembers=[...candidate?.querySelectorAll('.work-record-items > article')||[]];previousMembers=[...host.querySelectorAll('.work-record-items > article')];measured=candidate?{open:candidate.querySelector('.work-records').open,height:candidate.getBoundingClientRect().height}:null;if(!changed){changed=true;memory.set('scope'+group.viewKey,{open:latest});if(rapid)queueMicrotask(()=>memory.set('scope'+group.viewKey,{open:initial}));}},afterCommit(){commits++;if(measured){const live=host.querySelector('.work-record-group');if(live.querySelector('.work-records').open!==measured.open||Math.abs(live.getBoundingClientRect().height-measured.height)>=.6)throw Error('Group publication differs from measured open/geometry');for(const row of live.querySelectorAll('.work-record-items > article'))if(!previousMembers.includes(row)&&!preparedMembers.includes(row))throw Error('New member root was not measured in this batch');trace.push({kind:'geometry',measured:measured.height,committed:live.getBoundingClientRect().height});}trace.push({kind:'commit',open:host.querySelector('.work-records')?.open,members:host.querySelectorAll('.work-record-items > article').length,memory:memory.get('scope'+group.viewKey).open});}};
   view=createWorkRecordWindow({scroller,host,memory,scope:'scope',locale:()=> 'en',actor:()=>actor,resolveIdentity:()=>({name:'Fixture'}),active:()=>true,requestRender(){automatic++;view.setMessages(messages,settings);},render(message,existing){
    const context=currentPreparedRender()?.context,phase=context?.phase;
    if(!baseline&&phase==='commit'&&!measurements.get(context)?.has(message.id))throw Error('Unmeasured member entered commit');
    const record=existing||{row:document.createElement('article'),tool:createToolCall({locale:()=> 'en',messageActor:()=>actor,resolveMessageIdentity:()=>({name:'Fixture'}),memory,key:message.id})};
    record.row.dataset.messageId=message.id;record.row.append(record.tool.element);record.tool.update(message);record.commit=()=>record;
    if(phase==='prepare'){if(!measurements.has(context))measurements.set(context,new Set());measurements.get(context).add(message.id);trace.push({kind:'prepare',id:message.id});}
    return record;
   },dispose(record){record.row.remove();}});
   try{
    view.setMessages(messages,settings);
    const deadline=performance.now()+3000;
    while(performance.now()<deadline&&(!view.prepared||commits<(rapid?1:2)))await new Promise(requestAnimationFrame);
    const final=host.querySelector('.work-records'),expected=rapid?initial:latest;
    if(baseline){results.push({initial,latest,trace,automatic,prepared:view.prepared});break;}
    if(!view.prepared||final?.open!==expected||memory.get('scope'+group.viewKey).open!==expected)throw Error('Latest disclosure intent was lost: '+JSON.stringify({trace,automatic,prepared:view.prepared}));
    if(!rapid&&automatic<1)throw Error('Changed intent needed an extra user action');
    const member=host.querySelector('.work-record-items > article');
    if(expected&&!member)throw Error('Expanded group has no prepared member');
    results.push({initial,latest,rapid,final:final.open,memory:memory.get('scope'+group.viewKey).open,automatic,commits,trace});
   }finally{view.close();scroller.remove();}
  }
  return results;
 },!!process.env.POT_GROUP_BASELINE);
 if(process.env.POT_GROUP_BASELINE)assert(errors.some(error=>error.includes('Prepared render slot does not match its committed revision')),'Original group must reproduce the production missing-slot failure');else assert.deepEqual(errors,[]);console.log(JSON.stringify({baseline:!!process.env.POT_GROUP_BASELINE,results,errors}));
}finally{await browser?.close();await source.close();}
