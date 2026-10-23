// Reproducible product screenshots and interaction checks using synthetic Chat.
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const [origin='http://127.0.0.1:17729',directory='output/pr-images/chat-bubbles-captures']=process.argv.slice(2);
await mkdir(directory,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
const open=async(version,{width=1280,height=1200,theme='light',scenario='conversation',locale='en'}={})=>{
  const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:1,colorScheme:theme,reducedMotion:'reduce',permissions:['clipboard-read','clipboard-write']});
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(`${origin}/${version}/scripts/tests/fixtures/chat-bubbles.html?locale=${locale}&theme=${theme}&scenario=${scenario}`);
  await page.waitForFunction(()=>!!window.chatBubbles);
  if(width<720)await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
  await page.evaluate(()=>window.chatBubbles.ready());
  await page.evaluate(()=>Promise.all([...document.images].map(image=>image.decode())));
  assert.deepEqual(errors,[]);return page;
};
const fit=async page=>{
  const result=await page.evaluate(()=>({document:document.documentElement.scrollWidth<=innerWidth,history:document.querySelector('.history').scrollWidth<=document.querySelector('.history').clientWidth,rows:[...document.querySelectorAll('.message')].every(row=>row.getBoundingClientRect().right<=innerWidth),unsafe:!!window.bubblesInjected,errors:window.chatBubbles.errors}));
  assert.deepEqual(result,{document:true,history:true,rows:true,unsafe:false,errors:[]});
};
try{
  for(const options of [
    {name:'chat-desktop-light',width:1280,height:1200},
    {name:'chat-phone-dark',width:390,height:1100,theme:'dark',locale:'zh-CN'},
    {name:'chat-rich-desktop-light',width:1280,height:1400,scenario:'rich'},
    {name:'chat-failure-phone-dark',width:390,height:1000,theme:'dark',scenario:'failure',locale:'zh-CN'},
  ])for(const version of ['before','after']){
    const page=await open(version,options);await fit(page);
    assert.equal(await page.locator('.role-user > :not(.message-body):not(.copy-message):not(.guidance-received)').count(),0,'No user identity row');
    if(version==='after'){
      assert.ok(await page.locator('.role-assistant > .message-speaker').count());
      assert.equal(await page.locator('.role-assistant .reply-bubble').first().evaluate(el=>getComputedStyle(el).backgroundColor),'rgba(0, 0, 0, 0)');
      const button=page.locator('.role-assistant > .copy-message').first();
      assert.ok(await button.evaluate(el=>el.getBoundingClientRect().top>=el.previousElementSibling.getBoundingClientRect().bottom),'Copy follows body');
    }
    if(options.scenario==='rich'){
      assert.equal(await page.locator('.tool-call[open]').count(),0,'Communication stays collapsed by default');
      await page.locator('pot-resource').last().waitFor({state:'visible'});
      await page.waitForFunction(()=>[...document.querySelectorAll('pot-resource')].every(el=>el.dataset.state==='ready'));
      assert.equal(await page.locator('.background-task').count(),1);
    }
    await page.screenshot({path:resolve(directory,`${options.name}-${version}.png`)});
    console.log(`Captured ${options.name}-${version}`);await page.close();
  }
  // Natural Markdown, exact copying, keyboard access and width/theme reflow.
  const page=await open('after');
  const copy=page.locator('[data-message-id="long"] > .copy-message');
  await copy.focus();await copy.press('Enter');
  assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),await page.evaluate(()=>window.chatBubbles.prose));
  await page.getByRole('button',{name:'Settings',exact:true}).click();
  await page.getByRole('button',{name:'Dark',exact:true}).click();
  await page.getByRole('combobox',{name:'Language',exact:true}).selectOption('zh-CN');
  await page.getByRole('button',{name:'聊天',exact:true}).click();
  assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');
  await page.setViewportSize({width:390,height:1100});
  if(await page.locator('.assistant-row').first().isVisible())await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
  await page.evaluate(()=>window.chatBubbles.ready());
  await page.waitForFunction(()=>getComputedStyle(document.querySelector('.reply-bubble')).fontSize==='14px');
  await fit(page);
  assert.equal(await page.locator('.reply-bubble').first().evaluate(el=>getComputedStyle(el).fontSize),'14px');
  if(await page.locator('.latest-button').isVisible())await page.locator('.latest-button').click();
  await page.evaluate(()=>window.chatBubbles.stream('Streaming synthetic reply…'));
  const stream=await page.locator('[data-message-id="stream"] .reply-content').elementHandle();
  await page.evaluate(()=>window.chatBubbles.stream('Streaming synthetic reply… with more retained text.'));
  assert.ok(await stream.evaluate(el=>el.isConnected),'Streaming retains its body node');
  await page.getByRole('button',{name:'停止',exact:true}).click();
  await page.waitForFunction(()=>window.chatBubbles.cancelCount===1);
  await fit(page);await page.close();
  // Partial failures retain readable output, diagnostics and no remote retry.
  for(const theme of ['dark','light']){
  const failure=await open('after',{scenario:'failure',width:390,height:1000,locale:theme==='dark'?'zh-CN':'en',theme});
  assert.equal(await failure.locator('.reply-action').count(),0);
  await failure.locator('.reply-error-details > summary').click();
  assert.match(await failure.locator('.reply-error-details').innerText(),/HTTP 503/);
  await fit(failure);
  await failure.locator('.composer-input').fill('Synthetic local retry');
  await failure.locator('.send-button').click();
  await failure.waitForFunction(()=>window.chatBubbles.sendCount===1);
  await failure.locator('.reply-action').waitFor({state:'visible'});
  await failure.locator('.reply-action').focus();await failure.locator('.reply-action').press('Enter');
  await failure.waitForFunction(()=>window.chatBubbles.sendCount===2);
  await fit(failure);await failure.close();
  }
  for(const width of [1280,390]){
  const rich=await open('after',{scenario:'rich',width});
  for(const id of ['review-send','review-receive']){
    const card=rich.locator(`[data-call-id="${id}"]`);
    await card.locator(':scope > summary').focus();await card.locator(':scope > summary').press('Enter');
    await card.locator('.communication-text strong').waitFor();
    assert.equal(await card.locator('.tool-raw').evaluate(el=>el.open),false);
    await card.locator('.tool-raw > summary').click();
    assert.equal(await card.locator('.tool-raw').evaluate(el=>el.open),true);
    await fit(rich);
  }
  await rich.locator('.message-reference-open').click();
  await rich.locator('[data-message-id="source"].reference-target').waitFor();
  await rich.locator('.message-attachments .resource-image').click();
  assert.equal(await rich.locator('.message-attachments pot-resource').evaluate(el=>el.classList.contains('is-expanded')),true);
  await rich.locator('[data-message-id="resource"] .resource-actions button').last().click();
  assert.equal(await rich.evaluate(()=>window.chatBubbles.downloads),1);
  await fit(rich);
  await rich.locator('.background-task').click();
  await rich.locator('.task-conversation').waitFor();
  await rich.close();
  }
  console.log('Chat bubbles: safe Markdown, identity, copy, theme/locale changes, phone reflow, stable streaming, stop/retry, diagnostics, references, attachments, resources, Task entrance and communication passed');
}finally{await browser.close();}
