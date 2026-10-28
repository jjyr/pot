import {createMessageWindow} from '/components/message-window.mjs';
import {createReplyBubble} from '/components/reply.mjs';
import {currentPreparedRender,retirePreparedTree} from '/components/prepared-render.mjs';
await document.fonts.ready;
const app=document.querySelector('#app');app.className='pot-app';app.style.cssText='display:block;height:600px';
const scroller=document.createElement('div');scroller.className='history';scroller.style.cssText='width:700px;height:550px';const column=document.createElement('div');column.className='message-column';const host=document.createElement('div');host.className='message-window';column.append(host);scroller.append(column);app.append(scroller);
const scenario=new URL(location.href).searchParams.get('scenario')||'commit',preparationCalls=new WeakMap(),evidenceKey=location.pathname.slice(1);
let acquired=0,released=0,interrupt=false,ui,rows=[{id:'failure-row',viewKey:'failure-row',role:'assistant',text:'Initial stable message',status:'complete',phase:'complete'}];
const errors=[],checks=[],leases=[];
const resource={id:'failure-resource',name:'synthetic.md',entry:'synthetic.md',mime_type:'text/markdown',files:[],size:30},options={t:key=>key,acquireResource:()=>{acquired++;const lease={released:false};leases.push(lease);return {read:async()=>new Blob(['# Prepared retained resource\n\nA synthetic resource used only by this test.']),release(){if(lease.released)throw Error('Synthetic lease released twice');lease.released=true;released++;}};},downloadResource:async()=>{}};
function render(message,old){if(!old){const row=document.createElement('article');row.className='message role-assistant';row.dataset.messageId=message.id;const body=document.createElement('div');body.className='message-body';row.append(body);old={row,reply:createReplyBubble(body)};}old.reply.update(message,options);const environment=currentPreparedRender();if(interrupt&&scenario==='commit'&&environment?.context.phase==='commit')throw Error('Synthetic independent commit interruption');if(interrupt&&scenario!=='commit'&&environment?.context.phase==='prepare'&&old.row.isConnected){const count=(preparationCalls.get(environment.context)||0)+1;preparationCalls.set(environment.context,count);if(count>=(scenario==='prepare-raf'?2:1))throw Error('Synthetic independent preparation interruption');}return old;}
function draw(){ui.setMessages(rows);}ui=createMessageWindow({scroller,host,render,dispose:record=>retirePreparedTree(record.row),requestRender:draw});
addEventListener('error',event=>errors.push({at:performance.now(),message:event.message,stack:event.error?.stack}));
const frame=()=>new Promise(requestAnimationFrame),wait=async(predicate,label)=>{const start=performance.now();while(!predicate()){if(performance.now()-start>12000)throw Error('Timeout '+label);await frame();}};
function sample(label){const value={label,at:performance.now(),prepared:ui.prepared,busy:host.getAttribute('aria-busy'),pending:host.hasAttribute('data-prepared-pending'),surfaces:document.querySelectorAll('[data-preparation-surface]').length,liveResources:host.querySelectorAll('pot-resource').length,acquired,released};checks.push(value);return value;}
let failure;
try{
 draw();await wait(()=>ui.prepared,'initial');sample('initial');
 rows=[{...rows[0],text:'After connected hidden resource preparation',presentation:{resource,mode:'preview'}}];interrupt=true;draw();
 await wait(()=>errors.some(error=>error.message.includes('Synthetic independent '+(scenario==='commit'?'commit':'preparation')+' interruption')),'rethrow observed');await frame();
 const interrupted=sample('interrupted');if(interrupted.prepared||interrupted.surfaces||interrupted.busy==='true'||interrupted.pending)throw Error('Failed transaction retained a prepared state or unfinished surface');
 if(acquired-released!==interrupted.liveResources)throw Error('A failed preparation retained a lease outside the actual live resource tree');
 interrupt=false;draw();await wait(()=>ui.prepared&&host.querySelector('pot-resource[data-state="ready"]'),'retry');sample('recovered');
 if(errors.some(error=>!error.message.includes('Synthetic independent '+(scenario==='commit'?'commit':'preparation')+' interruption')))throw Error('Unexpected renderer error');
 ui.close();await Promise.resolve();sample('closed');if(acquired!==released||document.querySelector('[data-preparation-surface]'))throw Error('Retired transaction leaked a lease or surface');
}catch(error){failure=error.stack;ui.close();}
const result={scenario,failure:failure||null,errors,checks,acquired,released};window.preparedFailureResult=result;document.querySelector('#results').textContent=JSON.stringify({failure:result.failure,errors:result.errors,acquired,released,done:true},null,2);
