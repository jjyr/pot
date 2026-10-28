import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {sourceServer,launchBrowser} from './browser-harness.mjs';

// A retained candidate can outlive one preparation context. The next context
// must own both summary and relation slots before updating an older live view.
const source=await sourceServer();let browser;
try{
 browser=await launchBrowser();const page=await browser.newPage(),errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 if(process.env.POT_COMMUNICATION_BASELINE){
  const body=await readFile(process.env.POT_COMMUNICATION_BASELINE,'utf8');
  await page.route('**/components/communication.mjs',route=>route.fulfill({contentType:'text/javascript',body}));
 }
 await page.goto(source.origin+'/scripts/tests/fixtures/prepared-empty.html');
 const results=await page.evaluate(async baseline=>{
  const {createCommunication}=await import('/components/communication.mjs');
  const {createPreparedRender,withPreparedRender,settlePreparedTree}=await import('/components/prepared-render.mjs');
  const results=[];await document.fonts.ready;
  for(const slot of ['summary','relation']){
   let name='Old recipient';
   const options={locale:()=> 'en',resolveIdentity:()=>({name})};
   const live=createCommunication(options),candidate=createCommunication(options);
   const model={kind:'send',messages:[{from:{node_id:'fixture',kind:'assistant',agent_id:'from'},to:{node_id:'fixture',kind:'assistant',agent_id:'to'},text:'Synthetic retained body'}],count:1,sources:[]};
   const root=document.createElement('div'),hidden=document.createElement('div');
   root.style.cssText='width:400px';hidden.style.cssText='position:fixed;left:0;top:0;width:400px;visibility:hidden;pointer-events:none';
   root.append(live.summary,live.body);hidden.append(candidate.summary,candidate.body);document.body.append(root,hidden);
   live.update(model,{expanded:slot==='relation'});name='New recipient';
   const first=createPreparedRender();
   withPreparedRender(first,'mail',()=>candidate.update(model,{expanded:slot==='relation'}));
   if(slot==='relation')live.update(model,{expanded:false});
   const next=createPreparedRender();next.root=hidden;
   withPreparedRender(next,'mail',()=>candidate.update(model,{expanded:slot==='relation'}));
   await settlePreparedTree(hidden,next);
   const projection=slot==='summary'?candidate.summary:candidate.body.querySelector('.communication-identity');
   const nodes=[...projection.childNodes],box=projection.getBoundingClientRect();
   let failure;next.phase='commit';
   try{withPreparedRender(next,'mail',()=>live.update(model,{expanded:slot==='relation'}));}catch(error){failure=error.message;}
   if(baseline){
    if(!failure?.includes('mail:dom:communication-'+slot))throw Error('Baseline did not reproduce missing '+slot+': '+failure);
    results.push({slot,expectedFailure:failure});
   }else{
    if(failure)throw Error(failure);
    const target=slot==='summary'?live.summary:live.body.querySelector('.communication-identity'),committed=target.getBoundingClientRect();
    if(!nodes.length||nodes.some((node,index)=>target.childNodes[index]!==node))throw Error('Commit replaced measured '+slot+' DOM');
    if(Math.abs(box.height-committed.height)>=.6||Math.abs(box.width-committed.width)>=.6)throw Error('Committed '+slot+' geometry differs from measured DOM');
    results.push({slot,sameDOM:true,measured:{width:box.width,height:box.height},committed:{width:committed.width,height:committed.height}});
   }
   root.remove();hidden.remove();
  }
  return results;
 },!!process.env.POT_COMMUNICATION_BASELINE);
 assert.deepEqual(errors,[]);console.log(JSON.stringify({baseline:!!process.env.POT_COMMUNICATION_BASELINE,results}));
}finally{await browser?.close();await source.close();}
