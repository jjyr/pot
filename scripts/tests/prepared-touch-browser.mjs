import {observePreparedGeometry} from './prepared-geometry-observer.mjs';
import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {preparedChatFixture} from './prepared-chat-fixture.mjs';
const baseline=process.argv.includes('--baseline'),controlCalls=[];
const output=process.env.POT_TOUCH_OUTPUT||'.pot-artifacts/prepared-touch';await mkdir(output,{recursive:true});
const source=await sourceServer({root:process.env.POT_TOUCH_SOURCE||process.cwd()}),fixture=await preparedChatFixture(source.origin),browser=await launchBrowser();
try{
 const control=async value=>{controlCalls.push({at:Date.now(),value});const response=await fetch(fixture.origin+'/fixture/control',{method:'POST',body:JSON.stringify(value)});assert(response.ok);return response.json();};
 await control({scenario:'prose'});
 const context=await browser.newContext({viewport:{width:390,height:852},isMobile:true,hasTouch:true,serviceWorkers:'block'}),page=await context.newPage(),errors=[];await observePreparedGeometry(page);page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));
 await context.route('**/*',route=>new URL(route.request().url()).origin===fixture.origin?route.continue():route.abort());
 await context.addInitScript(()=>{
  window.touchEvidence={timeOrigin:performance.timeOrigin,phase:'initial',frames:[],events:[],writes:[],mutations:[],measures:[]};const data=touchEvidence;
  const descriptor=Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop');
  Object.defineProperty(Element.prototype,'scrollTop',{...descriptor,set(value){if(this.classList?.contains('history'))data.writes.push({at:performance.now(),phase:data.phase,from:descriptor.get.call(this),to:value,extent:this.scrollHeight,stack:new Error().stack});descriptor.set.call(this,value);}});
  for(const type of ['touchstart','touchmove','touchend','touchcancel','pointerdown','pointermove','pointerup','pointercancel','scroll','scrollend'])document.addEventListener(type,event=>{const h=document.querySelector('.history:not([data-preparation-surface])');if(h&&(event.target===h||h.contains(event.target)))data.events.push({type,trusted:event.isTrusted,at:performance.now(),phase:data.phase,top:h.scrollTop,extent:h.scrollHeight,touches:[...event.touches||[]].map(t=>({x:t.clientX,y:t.clientY}))});},{capture:true,passive:true});
  const tick=()=>{const h=document.querySelector('.history:not([data-preparation-surface])'),b=h?.getBoundingClientRect(),rows=[...h?.querySelectorAll('article.message')||[]],visible=rows.filter(row=>{const r=row.getBoundingClientRect();return r.bottom>b.top&&r.top<b.bottom;});data.frames.push({at:performance.now(),phase:data.phase,top:h?.scrollTop,extent:h?.scrollHeight,height:h?.clientHeight,latest:document.querySelector('.latest-button')?.hidden,rows:visible.map(row=>({key:row.dataset.windowKey,id:row.dataset.messageId,top:row.getBoundingClientRect().top,height:row.getBoundingClientRect().height,type:row.querySelector('pot-mermaid')?'diagram':row.querySelector('pre')?'code':'prose',state:row.querySelector('pot-mermaid')?.dataset.state,unfinished:row.querySelector('pot-mermaid')?.dataset.unfinished,sourceOpen:row.querySelector('.mermaid-source')?.open,measured:preparedGeometry.rows[row.dataset.messageId]?.height}))});requestAnimationFrame(tick);};requestAnimationFrame(tick);
 });
 await page.route('**/components/prepared-render.mjs',async route=>{const response=await route.fetch();const source=await response.text(),marker='finish();resolve({height';assert(source.includes(marker));const observe="window.touchEvidence?.measures.push({at:performance.now(),id:root.dataset.messageId,type:root.querySelector('pot-mermaid')?'diagram':root.querySelector('pre')?'code':'prose',height:box.height,state:root.querySelector('pot-mermaid')?.dataset.state,unfinished:root.querySelector('pot-mermaid')?.dataset.unfinished});";await route.fulfill({response,body:source.replace(marker,observe+marker)});});
 await page.goto(fixture.origin+'/?locale=en&theme=light&assistant_id=alpha');await page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&getComputedStyle(document.querySelector('.history:not([data-preparation-surface])')).visibility==='visible');const dismiss=page.getByRole('button',{name:'Not now',exact:true});if(await dismiss.count())await dismiss.click();
 const cdp=await context.newCDPSession(page);
 for(let attempt=0;attempt<1;attempt++){
  await page.evaluate(attempt=>touchEvidence.phase='setup-'+attempt,attempt);
  const openText='A retained progress paragraph.\n\n```mermaid\ngraph TD\n A[Prepare] --> B[Commit]\n'+Array.from({length:60},(_,i)=>'%% Synthetic source comment '+i).join('\n');
  await control({scenario:'custom',text:openText,holdPreview:true});
  await page.waitForTimeout(250);
  await page.evaluate(attempt=>touchEvidence.phase='setup-'+attempt,attempt);const box=await page.locator('.history:not([data-preparation-surface])').boundingBox();await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.wheel(0,-100);await page.waitForFunction(()=>!document.querySelector('.latest-button').hidden);await page.evaluate(async()=>{for(let i=0;i<12;i++)await new Promise(requestAnimationFrame);});
  await page.evaluate(attempt=>touchEvidence.phase='fling-'+attempt,attempt);
  const point={x:Math.round(box.x+box.width/2),y:Math.round(box.y+box.height*.8)};
  await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[point]});
  let move=0;
  for(const displacement of [45,120,220,340,440,510]){
   if(move===1)await control({scenario:'custom',text:openText+'\n```'});move++;await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{...point,y:point.y-displacement}]});await page.evaluate(()=>new Promise(requestAnimationFrame));}
  await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  await page.evaluate(async()=>{for(let i=0;i<60;i++)await new Promise(requestAnimationFrame);});
 }
 const evidence=await page.evaluate(()=>touchEvidence);assert.deepEqual(errors,[]);assert(evidence.events.some(row=>row.type==='touchmove'),'Actual touch movement must occur');assert(evidence.frames.some((row,i)=>i&&row.phase.startsWith('fling-')&&row.top>evidence.frames[i-1].top),'Probe must actually scroll downward');
 await writeFile(output+'/raw.json',JSON.stringify({baseline,evidence,http:fixture.trace,controlCalls,errors},null,2));
 const summary=[0].map(attempt=>{const name='fling-'+attempt,frames=evidence.frames.filter(frame=>frame.phase===name),writes=evidence.writes.filter(row=>row.phase===name),events=evidence.events.filter(row=>row.phase===name),reverse=frames.slice(1).map((row,i)=>({at:row.at,delta:row.top-frames[i].top,from:frames[i].top,to:row.top,extent:row.extent})).filter(row=>row.delta<-.5);return {name,first:frames[0],last:frames.at(-1),reverse,writes,touchEvents:events.filter(row=>row.type.startsWith('touch')).length,trustedTouches:events.filter(row=>row.type.startsWith('touch')&&row.trusted).length,scrollends:events.filter(row=>row.type==='scrollend').length};});
 const gesture=summary[0];
 if(baseline){assert(evidence.frames.some(frame=>frame.rows.some(row=>row.id==='active-answer'&&row.type==='code'&&row.height>1000)),'Baseline must publish the temporary long source');assert(gesture.reverse.length,'Baseline must reproduce the shrink/clamp failure');}
 else{assert.equal(gesture.reverse.length,0,'Prepared diagram publication must not reverse the downward gesture or its tail');assert(evidence.events.some(event=>event.phase==='fling-0'&&event.type==='touchend'),'The original native target must survive through touchend');assert(!evidence.frames.some(frame=>frame.rows.some(row=>row.id==='active-answer'&&row.type==='code'&&row.height>1000)),'Known unfinished diagrams cannot first publish long source');assert(evidence.measures.some(item=>item.id==='active-answer'&&item.type==='diagram'&&item.state==='ready'),'A ready diagram must be measured before publishing');}
 await page.screenshot({path:output+'/'+(baseline?'before':'after')+'.png'});
 await writeFile(output+'/summary.json',JSON.stringify({baseline,expectedBaselineFailure:baseline,summary},null,2));console.log(JSON.stringify(summary.map(({name,reverse,writes,touchEvents,trustedTouches,scrollends})=>({name,reverse,writes,touchEvents,trustedTouches,scrollends})),null,2));await context.close();
}finally{await browser.close();await fixture.close();await source.close();}
