// Real Task renderer, synthetic transcript only; all business writes are rejected.
import {createTaskConversation} from '../../web/src/components/task-conversation.mjs';
import {createTaskTranscript} from '../../web/src/state/task-transcript.mjs';
const root=document.querySelector('#app'),results=document.querySelector('#results'),errors=[];
addEventListener('error',event=>errors.push(event.message));addEventListener('unhandledrejection',event=>errors.push(String(event.reason)));
const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
const wait=async predicate=>{for(let i=0;i<180;i++){if(predicate())return;await frame();}throw Error('Timed out: '+predicate);};
const check=(condition,text)=>{if(!condition)throw Error(text);results.textContent+='PASS '+text+'\n';};
const scope={space:'fixture',owner:'fixture'},task={id:'synthetic-task',address:{node_id:'fixture',kind:'task',agent_id:'synthetic-task'},phase:'running'};
const client={runtime:{runtime_id:'fixture'},spaces:[{id:'fixture',control_id:'fixture'}],scope:()=>scope,changes:()=>()=>{},request:async(path,options={})=>{if(options.method&&options.method!=='GET')throw Error('Business writes forbidden');return {items:[]};}};
const tool=(id,parent='')=>({id,role:'tool',text:'',status:'complete',tool:{id,turn_id:'turn',parent_id:parent,name:'fixture_tool',phase:'completed',args:{value:id},output:[]}});
const prose=id=>({id,role:'assistant',text:'Synthetic prose separates virtual work records. '.repeat(6),phase:'complete'});
async function run(){
 results.textContent='';
 for(const split of [false,true]){
  const parent=tool('parent');parent.tool.script={failureID:'target'};parent.tool.diagnostic='Synthetic nested failure';
  const target=tool('target','parent'),state=createTaskTranscript();
  state.messages=[parent,...(split?Array.from({length:80},(_,i)=>prose('between-'+i)):[]),target,...Array.from({length:100},(_,i)=>prose('after-'+i))];
  state.messages.forEach((message,index)=>message.sequence=index+1);
  const view=createTaskConversation({root,task,record:{info:task,request:{text:'Synthetic navigation task'},capabilities:{version:1,operations:['task.read']}},locale:()=> 'en',client,scope,draft:'',onDraft:()=>{},toolMemory:new Map(),approvalFor:()=>null,assistantName:'Synthetic coordinator'});
  try{
   view.update({state});view.start();await frame();
   const history=root.querySelector('.task-history');history.dispatchEvent(new WheelEvent('wheel',{deltaY:-100000,bubbles:true}));history.scrollTop=0;history.dispatchEvent(new Event('scroll'));await frame();
   await wait(()=>root.querySelector('.work-record-summary'));
   root.querySelector('.work-record-summary').click();await wait(()=>root.querySelector('[data-call-id="parent"] .tool-failure'));
   if(split)check(!root.querySelector('[data-call-id="target"]'),'Cross-prose target starts outside the mounted virtual range');
   root.querySelector('[data-call-id="parent"] .tool-failure').click();
   await wait(()=>root.querySelector('[data-call-id="target"]')?.open);
   const destination=root.querySelector('[data-call-id="target"]'),group=destination.closest('.work-records'),box=destination.getBoundingClientRect(),viewport=history.getBoundingClientRect();
   check(group.open&&box.bottom>viewport.top&&box.top<viewport.bottom,(split?'Cross-prose':'Same-group')+' Task failure link mounts, opens and reveals its target');
   check(!split||!group.querySelector('[data-call-id="parent"]'),'Cross-prose navigation retains separate groups');
  }finally{view.dispose();root.replaceChildren();}
 }
 check(errors.length===0,'No browser errors: '+errors.join('; '));results.textContent+='ALL CHECKS PASSED';
}
run().catch(error=>{results.textContent+='FAIL '+error.message+'\n'+error.stack;});
