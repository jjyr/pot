// Actual production Chat with in-memory synthetic records; no model or commands.
import assert from 'node:assert/strict';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const origin=process.argv[2]||'http://127.0.0.1:17739';
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
try{
  for(const [width,theme,locale]of [[1280,'light','en'],[390,'dark','zh-CN'],[320,'light','en'],[1280,'system','zh-CN']]){
    const page=await browser.newPage({viewport:{width,height:1000},colorScheme:theme==='system'?'dark':theme,reducedMotion:'reduce'}),errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    await page.goto(`${origin}/scripts/tests/fixtures/task-communication.html?scenario=collapsed&locale=${locale}&theme=${theme}`);
    if(width<720)await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
    await page.evaluate(()=>window.taskCommunication.prepare());
    assert.equal(await page.locator('.communication-stack').count(),1);
    assert.match(await page.locator('.communication-stack-title').innerText(),/6.*7.*3/);
    assert.equal(await page.locator('.communication-stack-route').count(),5);
    assert.equal(await page.locator('.communication-entry').count(),0,'collapsed groups defer body rendering');
    await page.locator('.communication-stack-summary').focus();
    const headingTop=await page.locator('.communication-stack-summary').evaluate(node=>node.getBoundingClientRect().top);
    await page.keyboard.press('Enter');
    await page.waitForSelector('.communication-entry');
    await page.waitForTimeout(100);
    const expandedTop=await page.locator('.communication-stack-summary').evaluate(node=>node.getBoundingClientRect().top);
    assert(Math.abs(headingTop-expandedTop)<3,'manual expansion retains its visible heading');
    const result=await page.evaluate(async()=>{
      const {taskIdentities,taskColorIndex}=await import('/web/src/state/task-identity.mjs');
      const f=window.taskCommunication,stack=document.querySelector('.communication-stack');
      window.originalStack=stack;window.originalText=document.querySelector('[data-call-id="mail-3"] .communication-text p');
      const range=document.createRange();range.selectNodeContents(window.originalText);getSelection().removeAllRanges();getSelection().addRange(range);
      return {texts:[...document.querySelectorAll('.communication-text')].map(node=>node.textContent),colors:[...stack.querySelectorAll('.task-identity-mark')].map(mark=>mark.dataset.taskColor),color:taskColorIndex(taskIdentities.resolve(f.taskA)),short:taskIdentities.short(f.taskA),named:[...stack.querySelectorAll('.communication-name')].some(node=>node.textContent.includes('Layout review')),overflow:document.documentElement.scrollWidth>innerWidth};
    });
    assert.equal(result.texts.length,7);assert.equal(result.texts[2],'Check narrow screens and dark mode.');
    assert.equal(result.named,true);assert.equal(result.overflow,false);assert(result.colors.every(color=>color!==undefined));
    assert(result.short.includes('@'));assert(result.colors.includes(String(result.color)));
    await page.evaluate(()=>window.taskCommunication.update(messages=>{const f=window.taskCommunication;messages.splice(-1,0,{...f.send('append',f.taskA,'Appended message'),sequence:'8',version:1});}));
    assert.equal(await page.evaluate(()=>document.querySelector('.communication-stack')===originalStack&&document.querySelector('[data-call-id="mail-3"] .communication-text p')===originalText&&getSelection().toString()==='Check narrow screens and dark mode.'),true,'append retains group, body and selection');
    await page.evaluate(()=>window.taskCommunication.update(messages=>{const f=window.taskCommunication;messages.splice(1,0,{...f.receive('prepend',f.taskB,'Earlier message'),sequence:'1',version:1});}));
    assert.equal(await page.evaluate(()=>document.querySelector('.communication-stack')===originalStack&&document.querySelector('[data-call-id="mail-3"] .communication-text p')===originalText&&getSelection().toString()==='Check narrow screens and dark mode.'),true,'prepend retains group, body and selection');
    assert.match(await page.locator('.communication-stack-title').innerText(),/8.*9.*3/);
    await page.evaluate(()=>getSelection().removeAllRanges());
    const raw=page.locator('[data-call-id="mail-3"] .tool-raw');await raw.locator('summary').click();
    await page.locator('.communication-stack-title').click();await page.locator('.communication-stack-title').click();
    assert.equal(await raw.getAttribute('open'),'','secondary disclosure survives collapse');
    assert.equal(await page.locator('.communication-text').count(),9);
    await page.evaluate(()=>window.taskCommunication.update(messages=>{const i=messages.findIndex(message=>message.id==='mail-4');messages.splice(i,0,{id:'boundary',role:'assistant',status:'complete',text:'A real transcript boundary',sequence:'5',version:1});}));
    assert.equal(await page.locator('.communication-stack').count(),2,'assistant body splits the stack');
    await page.evaluate(()=>window.taskCommunication.update(messages=>{const item=messages.find(message=>message.id==='mail-6');item.tool={...item.tool,phase:'failed',diagnostic:'Synthetic transport failure'};item.version++;}));
    const failed=page.locator('[data-call-id="mail-6"]');
    assert.equal(await failed.locator('xpath=ancestor::*[contains(@class,"communication-stack")]').count(),0,'failure remains independent');
    assert.match(await failed.locator('.tool-status').innerText(),locale==='en'?/Failed/:/失败/);
    await page.evaluate(async()=>{const {taskIdentities}=await import('/web/src/state/task-identity.mjs');const f=window.taskCommunication;taskIdentities.register([{...f.catalogue[0],title:'Long task name · 超长任务名称 '.repeat(20)}]);await f.update(messages=>messages.splice(-1,0,{...f.send('missing',{node_id:'local',kind:'task'},'Missing identity'),sequence:'11',version:1}));});
    const missing=page.locator('[data-call-id="missing"]');
    assert.equal(await missing.locator('.task-identity-mark[data-task-color]').count(),0);
    assert.match(await missing.locator('.communication-short').innerText(),locale==='en'?/ID unavailable/:/ID 缺失/);
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'long names and missing identity fit');
    assert.deepEqual(errors,[]);console.log(`PASS Chat ${width}px ${theme} ${locale}: counts, identity, keyboard, append/prepend, selection, raw, boundaries, failure`);
    await page.close();
  }
  for(const fallback of [false,true]){
    const single=await browser.newPage({viewport:{width:1280,height:1000}});
    if(fallback)await single.addInitScript(()=>{Element.prototype.moveBefore=undefined;});
    await single.goto(`${origin}/scripts/tests/fixtures/task-communication.html?single=1&scenario=expanded`);
    await single.evaluate(()=>window.taskCommunication.prepare());
    await single.locator('[data-call-id="mail-1"] .tool-raw>summary').click();
    await single.evaluate(()=>{window.kept=document.querySelector('[data-call-id="mail-1"] .communication-text p');const range=document.createRange();range.selectNodeContents(kept);getSelection().removeAllRanges();getSelection().addRange(range);});
    await single.evaluate(()=>window.taskCommunication.update(messages=>{const f=window.taskCommunication,item=f.receive('second',f.taskA,'Second record');item.tool={...item.tool,args:'{"messages":',phase:'preparing'};messages.splice(2,0,{...item,sequence:'3',version:1});}));
    assert.equal(await single.locator('.communication-stack').count(),0,'streamed incomplete arguments stay independent');
    await single.evaluate(()=>window.taskCommunication.update(messages=>{const f=window.taskCommunication,item=messages[2];item.tool=f.receive('second',f.taskA,'Second record').tool;item.version++;}));
    assert.equal(await single.locator('.communication-stack').getAttribute('open'),'','an already open member opens its new group');
    assert.equal(await single.evaluate(()=>kept===document.querySelector('[data-call-id="mail-1"] .communication-text p')&&getSelection().toString()==='Please review the communication layout.'),true,'first grouping preserves text node and selection');
    assert.equal(await single.locator('[data-call-id="mail-1"] .tool-raw').getAttribute('open'),'');
    await single.evaluate(()=>window.taskCommunication.update(messages=>messages.splice(2,1)));
    assert.equal(await single.locator('.communication-stack').count(),0);
    assert.equal(await single.locator('[data-call-id="mail-1"]').getAttribute('open'),'','ungrouping retains the original member disclosure');
    console.log(`PASS first grouping and ungrouping: retained body, selection, raw (${fallback?'WebView fallback':'moveBefore'})`);
    await single.close();
  }
  // Recycling and size corrections run through the real Chat virtualizer.
  const virtual=await browser.newPage({viewport:{width:1280,height:900}});
  await virtual.goto(`${origin}/scripts/tests/fixtures/task-communication.html?scenario=expanded`);
  await virtual.evaluate(()=>window.taskCommunication.prepare());
  await virtual.locator('[data-call-id="mail-3"] .tool-raw>summary').click();
  await virtual.evaluate(()=>window.taskCommunication.update(messages=>{
    for(let index=0;index<120;index++)messages.push({id:'filler-'+index,role:'assistant',text:'Synthetic history paragraph '+index+' — '.repeat(80),status:'complete',sequence:String(index+20),version:1});
  }));
  await virtual.locator('[data-call-id="mail-3"]').scrollIntoViewIfNeeded();
  await virtual.mouse.move(900,400);await virtual.mouse.wheel(0,100);
  await virtual.waitForFunction(()=>document.querySelector('.history').scrollTop>0);
  await virtual.waitForTimeout(250);
  const before=await virtual.locator('[data-call-id="mail-3"]').evaluate(node=>node.getBoundingClientRect().top);
  await virtual.evaluate(()=>window.taskCommunication.update(messages=>{
    const f=window.taskCommunication;messages.splice(1,0,{...f.receive('history-prepend',f.taskB,'Prepended history'),sequence:'1',version:1});
  }));
  await virtual.waitForTimeout(250);
  const after=await virtual.locator('[data-call-id="mail-3"]').evaluate(node=>node.getBoundingClientRect().top);
  assert(Math.abs(before-after)<3,`history prepend keeps the reading offset: ${before} -> ${after}`);
  await virtual.evaluate(()=>{document.activeElement.blur();getSelection().removeAllRanges();const history=document.querySelector('.history');history.dispatchEvent(new WheelEvent('wheel',{deltaY:100000,bubbles:true}));history.scrollTop=history.scrollHeight;history.dispatchEvent(new Event('scroll'));});
  await virtual.waitForFunction(()=>!document.querySelector('.communication-stack'));
  assert(await virtual.locator('.history article.message').count()<80,'offscreen transcript rows recycle');
  await virtual.evaluate(()=>{const history=document.querySelector('.history');history.dispatchEvent(new WheelEvent('wheel',{deltaY:-100000,bubbles:true}));history.scrollTop=0;history.dispatchEvent(new Event('scroll'));});
  await virtual.waitForSelector('[data-call-id="mail-3"] .tool-raw[open]');
  assert.equal(await virtual.locator('.communication-stack').getAttribute('open'),'','stack expansion survives virtualization');
  console.log('PASS actual Chat virtualization: history reading offset, bounded rows, stack and raw restoration');
  await virtual.close();
  const taskPage=await browser.newPage({viewport:{width:1280,height:1000}});
  await taskPage.goto(`${origin}/scripts/tests/fixtures/task-communication.html?scenario=expanded`);
  await taskPage.evaluate(()=>window.taskCommunication.prepare());
  const taskResult=await taskPage.evaluate(async()=>{
    const {createTaskConversation}=await import('/web/src/components/task-conversation.mjs');
    const {createTaskTranscript}=await import('/web/src/state/task-transcript.mjs');
    const f=window.taskCommunication,root=document.createElement('section');root.style.cssText='position:fixed;inset:0';document.body.append(root);
    const state=createTaskTranscript();state.messages=f.messages.slice(1,-1);
    let locale='en',approval=null;
    const view=createTaskConversation({root,task:f.catalogue[0],record:{info:f.catalogue[0],request:{text:'Synthetic task communication'},capabilities:{version:1,operations:['task.input']}},locale:()=>locale,client:f.client,scope:f.client.scope(),draft:'Retained draft',onDraft:()=>{},toolMemory:new Map(),approvalFor:tool=>tool.id==='mail-3'?approval:null,onDecision:()=>{throw Error('Fixture rejects approval mutations');},assistantName:'Pot'});
    const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
    view.update({state});view.start();await frame();
    root.querySelector('.communication-stack-summary').click();await frame();
    const body=root.querySelector('.communication-text p'),stack=root.querySelector('.communication-stack'),editor=view.input;
    const shared=[...document.querySelectorAll('.communication-short')].filter(node=>node.textContent.includes('#8a1cf')).every(node=>node.textContent==='#8a1cf');
    locale='zh-CN';view.update({});await frame();
    const retained=stack===root.querySelector('.communication-stack')&&body===root.querySelector('.communication-text p')&&editor===view.input&&editor.value==='Retained draft';
    const localized=root.querySelector('.communication-stack-title').textContent.includes('条记录');
    approval={id:'synthetic-approval',request:{tool:'send_message',args:{to:f.taskB,text:'Synthetic pending approval'}}};view.update({});await frame();
    const pending=root.querySelector('[data-call-id="mail-3"]'),independent=!pending.closest('.communication-stack')&&pending.dataset.phase==='waiting_approval'&&!!pending.querySelector('.approval-card');
    view.dispose();root.remove();return {shared,retained,localized,independent};
  });
  assert.deepEqual(taskResult,{shared:true,retained:true,localized:true,independent:true});
  console.log('PASS Task conversation: shared identities, grouping, live locale, retained draft/body, independent approval');
  await taskPage.close();
  const page=await browser.newPage();
  for(const fixture of ['communication','message-titles']){
    await page.goto(`${origin}/scripts/tests/fixtures/${fixture}.html?phone=1&embedded=1`);
    await page.waitForFunction(()=>window.communicationResults||/^(PASS|FAIL)/.test(document.querySelector('#result')?.textContent));
    const text=await page.locator('#result').innerText();assert.match(text,/^PASS/);console.log(text);
  }
  await page.close();
}finally{await browser.close();}
