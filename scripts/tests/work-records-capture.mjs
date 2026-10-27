// Matched production components with identical synthetic records in two source trees.
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const [origin='http://127.0.0.1:18767',directory='output/pr-images/compact-work-records']=process.argv.slice(2);
await mkdir(directory,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
try{
 for(const [name,width,height,theme,scenario]of [
  ['chat-desktop-light-collapsed',1280,1000,'light','collapsed'],
  ['chat-desktop-light-expanded',1280,1100,'light','expanded'],
  ['chat-phone-dark-collapsed',390,900,'dark','collapsed'],
 ]){
  let source;
  for(const version of ['before','after']){
   const page=await browser.newPage({viewport:{width,height},colorScheme:theme,reducedMotion:'reduce',deviceScaleFactor:1}),errors=[];
   page.on('pageerror',error=>errors.push(error.message));
   await page.goto(`${origin}/${version}/scripts/tests/fixtures/work-records.html?locale=zh-CN&theme=${theme}`);
   await page.waitForFunction(()=>window.workRecordsFixture);
   if(width<720)await page.locator('.assistant-row').filter({hasText:'协调助手'}).click();
   await page.evaluate(()=>window.workRecordsFixture.prepare());
   if(scenario==='expanded'){
    await page.evaluate(()=>{for(const group of document.querySelectorAll('.work-records,.script-group,.communication-stack'))if(!group.open)group.querySelector('summary').click();});
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
   }
   const data=await page.evaluate(()=>JSON.stringify(window.workRecordsFixture.messages));
   if(source)assert.equal(data,source,'same source records');else source=data;
   await page.evaluate(()=>Promise.all([...document.images].map(image=>image.decode())));
   assert.deepEqual(errors,[]);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   if(version==='after'){
    assert.equal(await page.locator('.work-records').evaluate(node=>node.open),scenario==='expanded');
    assert.equal(await page.locator('.work-record-summary .work-participant').evaluateAll(nodes=>nodes.filter(node=>node.checkVisibility()).length),2);
   }
   await page.screenshot({path:resolve(directory,`${name}-${version}.png`)});
   console.log(`${name}-${version}: identical source, viewport, theme and disclosure level`);
   await page.close();
  }
 }
}finally{await browser.close();}
