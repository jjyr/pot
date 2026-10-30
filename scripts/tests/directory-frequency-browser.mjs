import assert from 'node:assert/strict';
import {readFile,mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {launchBrowser} from './browser-harness.mjs';

const origin=(await readFile(process.env.POT_DIRECTORY_FREQUENCY_URL_FILE,'utf8')).trim();
const imageOutput=resolve('output/pr-images/fix-task-output-refresh');await mkdir(imageOutput,{recursive:true});
const output=resolve(process.env.POT_DIRECTORY_FREQUENCY_OUTPUT||'.pot-artifacts/frequency');await mkdir(output,{recursive:true});
const browser=await launchBrowser(),results=[];
try{
 for(const phone of [false,true]){
  const context=await browser.newContext({viewport:phone?{width:390,height:852}:{width:1280,height:800},isMobile:phone,hasTouch:phone}),page=await context.newPage(),errors=[];
  page.setDefaultTimeout(15000);page.on('pageerror',error=>errors.push(error.message));
  await context.addInitScript(()=>{
   window.frequencyTrace=[];window.frequencyPhase='startup';
   const normalizePath=value=>{try{return new URL(value,location.href).pathname;}catch{return String(value).split('?')[0];}};
   {
    const NativeWebSocket=WebSocket,state=new WeakMap();
    window.WebSocket=class extends NativeWebSocket{
     constructor(...args){
      super(...args);
      const subscriptions=new Map(),seen=new Map();
      state.set(this,{subscriptions,seen});
      this.addEventListener('message',event=>{
       let message;try{message=JSON.parse(event.data);}catch{return;}
       if(message?.type!=='event')return;
       const socketState=state.get(this),id=message.id,path=socketState.subscriptions.get(id),eventName=message.event,data=message.data;
       let changed=[];
       if(eventName==='changed'&&data&&typeof data==='object'){
        let seenForSubscription=socketState.seen.get(id);
        if(!seenForSubscription){seenForSubscription=new Map();socketState.seen.set(id,seenForSubscription);}
        changed=Object.entries(data).filter(([key,value])=>seenForSubscription.get(key)!==value).map(([key,value])=>{seenForSubscription.set(key,value);return key;});
       }
       frequencyTrace.push({at:Date.now(),kind:'ws-event',phase:frequencyPhase,path,event:eventName,data,changed});
      });
     }
     send(data){
      const socketState=state.get(this);
      try{
       const message=JSON.parse(data);
       if(message?.type==='subscribe'){
        const id=message.id,path=message.path;
        if(id!==undefined&&path!==undefined)socketState.subscriptions.set(id,normalizePath(path));
       }else if(message?.type==='request'){
        const method=message.method,path=message.path;
        if(method!==undefined&&path!==undefined)frequencyTrace.push({at:Date.now(),kind:'ws-request',phase:frequencyPhase,method:String(method).toUpperCase(),path:normalizePath(path)});
       }
      }catch{}
      return super.send(data);
     }
    };
   }
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
     await page.screenshot({path:resolve(imageOutput,`task-output-${phone?'phone':'desktop'}-after.png`)});
    }
    await emitting;await page.waitForTimeout(350);
   }
   const raw=await page.evaluate(()=>frequencyTrace),server=await(await context.request.get(origin+'/fixture/state')).json();
   {const observedWsRequests=raw.filter(row=>row.kind==='ws-request'&&row.method==='GET'),observedWsChanged=raw.filter(row=>row.kind==='ws-event'&&row.event==='changed');assert(observedWsRequests.length>0,'observes actual WebSocket GET requests');assert(observedWsChanged.length>0,'observes WebSocket changed events');}
   const phases=raw.filter(row=>row.kind==='phase');
   for(let i=0;i<phases.length;i++){
    const start=phases[i].at,end=phases[i+1]?.at||Date.now(),name=phases[i].phase,rows=raw.filter(row=>start<=row.at&&row.at<end),frames=rows.filter(row=>row.kind==='frame'&&row.hasInput);
    let disabledMS=0;const windows=[];let windowStart;
    for(let j=0;j<frames.length;j++){if(frames[j].disabled){windowStart??=frames[j].at;disabledMS+=(frames[j+1]?.at||end)-frames[j].at;}else if(windowStart!==undefined){windows.push([windowStart-start,frames[j].at-start]);windowStart=undefined;}}
    if(windowStart!==undefined)windows.push([windowStart-start,end-start]);
    const wsRequests=rows.filter(row=>row.kind==='ws-request'&&row.method==='GET'),wsEvents=rows.filter(row=>row.kind==='ws-event'),wsChanged=wsEvents.filter(row=>row.event==='changed'),requestRows=wsRequests;
    const topicChanges={};for(const event of wsChanged)for(const topic of event.changed)topicChanges[topic]=(topicChanges[topic]||0)+1;
    results.push({phone,phase:name,durationMS:end-start,directoryGETs:requestRows.filter(row=>row.path==='/api/v1/assistants').length,taskGETs:requestRows.filter(row=>row.path.startsWith('/api/v1/tasks')).length,changeFrames:wsChanged.length,conversationFrames:wsEvents.filter(row=>row.path==='/api/v1/events').length,topicChanges,disabledMS,windows});
   }
   let gateEvidence=null;
   {
    const setGate=async hold=>{const response=await context.request.post(origin+'/fixture/directory-gate',{data:{hold}});assert(response.ok(),await response.text());};
    const pollGate=async predicate=>{const deadline=Date.now()+5000;while(Date.now()<deadline){const response=await context.request.get(origin+'/fixture/directory-gate');assert(response.ok(),await response.text());const body=await response.json();if(predicate(body))return body;await page.waitForTimeout(50);}throw new Error('directory gate poll timed out');};
    const messagePosts=rows=>rows.filter(row=>row.kind==='ws-request'&&row.method==='POST'&&(row.path==='/api/v1/messages'||row.path==='/api/v1/turn/steer'));
    try{
     await setGate(true);
     await emit('assistants',1,0);
     const held=await pollGate(body=>(body.held??0)>0);
     await phase('authority-held');
     const phaseStart=Date.now(),queued='Queued while directory held '+String(phone);
     await page.locator('#pot-composer').fill(queued);
     await page.locator('.send-button').click();
     await page.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
     await page.waitForTimeout(250);
     const preRelease=await page.evaluate(()=>frequencyTrace),preReleaseRows=preRelease.filter(row=>row.at>=phaseStart),preReleasePosts=messagePosts(preReleaseRows);
     assert.equal(preReleasePosts.length,0,'held directory blocks WS POST to messages/steer');
     await setGate(false);
     const releaseDeadline=Date.now()+15000;let releasedPosts=[];
     while(Date.now()<releaseDeadline){const snapshot=await page.evaluate(()=>frequencyTrace);releasedPosts=messagePosts(snapshot.filter(row=>row.at>=phaseStart));if(releasedPosts.length>=1)break;await page.waitForTimeout(100);}
     assert(releasedPosts.length>=1,'released directory flushes queued WS POST');
     await page.waitForTimeout(200);
     const postRelease=await page.evaluate(()=>frequencyTrace),postReleasePosts=messagePosts(postRelease.filter(row=>row.at>=phaseStart));
     assert.equal(postReleasePosts.length,1,'exactly one queued WS POST after release');
     gateEvidence={phone,phase:'authority-held',queued,held,preReleasePosts:preReleasePosts.length,releasedPosts:releasedPosts.length,postReleasePosts:postReleasePosts.map(row=>({at:row.at-phaseStart,method:row.method,path:row.path})),trace:postRelease.filter(row=>row.at>=phaseStart)};
    }finally{
     try{await setGate(false);}catch{}
    }
   }
   await writeFile(resolve(output,`frequency-${phone?'phone':'desktop'}.json`),JSON.stringify({results:results.filter(row=>row.phone===phone),server,ui:raw,errors,gate:gateEvidence},null,2));assert.deepEqual(errors,[]);
   const measured=name=>results.find(row=>row.phone===phone&&row.phase===name),stream=measured('transcript');
   assert(stream.durationMS>5000,'pure-output experiment crosses the resource TTL');
   assert.equal(stream.directoryGETs,0,'pure transcript output directory fanout');
   assert.equal(stream.disabledMS,0,'output alone keeps Send ready');
   assert.equal(stream.taskGETs,0,'output alone cannot refresh active Task cards');
   assert.equal(stream.topicChanges.runner,60);
   assert.equal(measured('usage').directoryGETs,0,'usage stays independent');
   for(const name of ['tasks','permissions','assistants']){assert.equal(measured(name).directoryGETs,5,name+' still refreshes directory');assert.equal(measured(name).disabledMS,0,name+' local submission ready');}
  }finally{await context.close();}
 }
 await writeFile(resolve(output,'summary.json'),JSON.stringify(results,null,2));console.log(JSON.stringify(results,null,2));
}finally{await browser.close();if(!process.argv.includes('--keep-fixture')){const response=await fetch(origin+'/fixture/stop',{method:'POST'});assert(response.ok,'fixture shutdown');}}
