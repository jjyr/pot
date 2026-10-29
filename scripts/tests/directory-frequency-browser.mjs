import assert from 'node:assert/strict';
import {readFile,mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {launchBrowser} from './browser-harness.mjs';

const origin=(await readFile(process.env.POT_DIRECTORY_FREQUENCY_URL_FILE,'utf8')).trim();
const baseline=process.argv.includes('--baseline'),imageOutput=resolve('output/pr-images/fix-task-output-refresh');await mkdir(imageOutput,{recursive:true});
const output=resolve(process.env.POT_DIRECTORY_FREQUENCY_OUTPUT||'.pot-artifacts/frequency');await mkdir(output,{recursive:true});
const browser=await launchBrowser(),results=[];
try{
 for(const phone of [false,true]){
  const context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},isMobile:phone,hasTouch:phone}),page=await context.newPage(),errors=[];
  page.setDefaultTimeout(15000);page.on('pageerror',error=>errors.push(error.message));
  await context.addInitScript(()=>{
   window.frequencyTrace=[];window.frequencyPhase='startup';
   const Native=EventSource;window.EventSource=class extends Native{constructor(url,options){super(url,options);const seen=new Map();this.addEventListener('changed',event=>{const data=JSON.parse(event.data),changed=Object.entries(data).filter(([key,value])=>seen.get(key)!==value).map(([key,value])=>{seen.set(key,value);return key;});frequencyTrace.push({at:Date.now(),kind:'sse',phase:frequencyPhase,path:new URL(url,location.href).pathname,data,changed});});}};
   const complete=Object.getOwnPropertyDescriptor(IDBTransaction.prototype,'oncomplete');Object.defineProperty(IDBTransaction.prototype,'oncomplete',{...complete,set(handler){complete.set.call(this,function(event){frequencyTrace.push({at:Date.now(),kind:'idb-complete',phase:frequencyPhase,stores:[...this.objectStoreNames],mode:this.mode});handler.call(this,event);});}});
   const sample=()=>{const button=document.querySelector('.send-button');frequencyTrace.push({at:Date.now(),kind:'frame',phase:frequencyPhase,disabled:button?.disabled,hasInput:!!document.querySelector('#pot-composer')?.value,connection:document.querySelector('.connection-status')?.textContent});requestAnimationFrame(sample);};requestAnimationFrame(sample);
  });
  const login=await context.request.post(origin+'/fixture/login');assert(login.ok());
  const ready=()=>page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&!document.querySelector('#pot-composer')?.disabled);
  const phase=name=>page.evaluate(name=>{window.frequencyPhase=name;frequencyTrace.push({at:Date.now(),kind:'phase',phase:name});},name);
  const emit=async(kind,count,interval_ms)=>{const response=await context.request.post(origin+'/fixture/emit',{data:{kind,count,interval_ms}});assert(response.ok(),await response.text());};
  try{
   await page.goto(origin+'/?locale=en');await page.locator('.assistant-row').filter({hasText:'History fixture'}).waitFor({state:'attached'});if(phone&&await page.locator('.assistant-row').filter({hasText:'History fixture'}).isVisible())await page.locator('.assistant-row').filter({hasText:'History fixture'}).click();await ready();
   if(await page.getByRole('button',{name:'Not now',exact:true}).isVisible())await page.getByRole('button',{name:'Not now',exact:true}).click();
   await page.locator('#pot-composer').fill('Synthetic retained input');await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);await page.waitForTimeout(300);
   await phase('idle');await page.waitForTimeout(900);
   await phase('main-output');await page.locator('#pot-composer').fill('Synthetic loopback model frequency');await page.locator('.send-button').click();await page.waitForFunction(()=>document.querySelector('#pot-composer').value==='');await page.locator('#pot-composer').fill('Draft during model output');await page.waitForTimeout(1000);
   await phase('main-output-steady');await page.locator('#pot-composer').fill('Second synthetic loopback response');await page.locator('.send-button').click();await page.waitForFunction(()=>document.querySelector('#pot-composer').value==='');await page.locator('#pot-composer').fill('Draft during steady model output');await page.waitForTimeout(1000);
   for(const [name,count,interval] of [['transcript',60,100],['usage',8,100],['tasks',5,160],['permissions',5,160],['assistants',5,160]]){
    await phase(name);const emitting=emit(name,count,interval);
    if(name==='transcript'){
     await page.waitForTimeout(300);
     if(baseline)await page.waitForFunction(()=>document.querySelector('.send-button')?.disabled);
     await page.screenshot({path:resolve(imageOutput,`task-output-${phone?'phone':'desktop'}-${baseline?'before':'after'}.png`)});
    }
    await emitting;await page.waitForTimeout(350);
   }
   const raw=await page.evaluate(()=>frequencyTrace),server=await(await context.request.get(origin+'/fixture/state')).json();
   const phases=raw.filter(row=>row.kind==='phase');
   for(let i=0;i<phases.length;i++){
    const start=phases[i].at,end=phases[i+1]?.at||Date.now(),name=phases[i].phase,rows=raw.filter(row=>start<=row.at&&row.at<end),frames=rows.filter(row=>row.kind==='frame'&&row.hasInput);
    let disabledMS=0;const windows=[];let windowStart;
    for(let j=0;j<frames.length;j++){if(frames[j].disabled){windowStart??=frames[j].at;disabledMS+=(frames[j+1]?.at||end)-frames[j].at;}else if(windowStart!==undefined){windows.push([windowStart-start,frames[j].at-start]);windowStart=undefined;}}
    if(windowStart!==undefined)windows.push([windowStart-start,end-start]);
    const http=server.trace.filter(row=>start<=row.at&&row.at<end&&row.kind==='http-start'),sse=rows.filter(row=>row.kind==='sse');
    const topicChanges={};for(const event of sse)for(const topic of event.changed)topicChanges[topic]=(topicChanges[topic]||0)+1;
    results.push({phone,phase:name,durationMS:end-start,directoryGETs:http.filter(row=>row.path==='/api/v1/assistants').length,taskGETs:http.filter(row=>row.path.startsWith('/api/v1/tasks')).length,changeFrames:sse.length,conversationFrames:server.trace.filter(row=>start<=row.at&&row.at<end&&row.kind==='sse'&&row.path==='/api/v1/events').length,topicChanges,disabledMS,windows});
   }
   await writeFile(resolve(output,`frequency-${phone?'phone':'desktop'}.json`),JSON.stringify({results:results.filter(row=>row.phone===phone),server,ui:raw,errors},null,2));assert.deepEqual(errors,[]);
   const measured=name=>results.find(row=>row.phone===phone&&row.phase===name),stream=measured('transcript');
   assert(stream.durationMS>5000,'pure-output experiment crosses the resource TTL');
   assert.equal(stream.directoryGETs,baseline?60:0,'pure transcript output directory fanout');
   if(baseline)assert(stream.disabledMS>0,'baseline reproduces disabled Send during transcript output');
   else {assert.equal(stream.disabledMS,0,'output alone keeps Send ready');assert.equal(stream.taskGETs,0,'output alone cannot refresh active Task cards');assert.equal(stream.topicChanges.runner,60);}
   assert.equal(measured('usage').directoryGETs,0,'usage stays independent');
   for(const name of ['tasks','permissions','assistants']){assert.equal(measured(name).directoryGETs,5,name+' still refreshes directory');assert(measured(name).disabledMS>0,name+' still fences commands');}
  }finally{await context.close();}
 }
 await writeFile(resolve(output,'summary.json'),JSON.stringify(results,null,2));console.log(JSON.stringify(results,null,2));
}finally{await browser.close();if(!process.argv.includes('--keep-fixture')){const response=await fetch(origin+'/fixture/stop',{method:'POST'});assert(response.ok,'fixture shutdown');}}
