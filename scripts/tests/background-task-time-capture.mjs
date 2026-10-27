// Dedicated elapsed fixture checks and matched PNG export, using the existing
// repository capture convention. Browser interaction review uses CUA separately.
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const [origin='http://127.0.0.1:17743',directory='output/pr-images/background-task-time',comparison='elapsed']=process.argv.slice(2);
const alignment=comparison==='alignment';
await mkdir(directory,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
try{
 for(const [width,theme,locale]of [[1280,'light','zh-CN'],[1280,'dark','en'],[390,'light','en'],[390,'dark','zh-CN'],[320,'light','zh-CN'],[320,'system','en']]){
  const page=await browser.newPage({viewport:{width,height:1100},colorScheme:theme==='system'?'dark':theme,reducedMotion:'reduce'}),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.goto(`${origin}/scripts/tests/fixtures/background-task-time.html?locale=${locale}&theme=${theme}`);
  await page.waitForFunction(()=>window.backgroundTaskTime);
  assert.deepEqual(await page.evaluate(()=>window.backgroundTaskTime.failures),[]);
  assert.deepEqual(errors,[]);
  console.log(`PASS ${width}px viewport ${theme} ${locale}: `+await page.locator('#result').textContent());
  await page.close();
 }
 // Exercise the final duration through the production Chat → Task detail path.
 {
  const page=await browser.newPage({viewport:{width:1280,height:1100}});
  await page.goto(`${origin}/scripts/tests/fixtures/task-communication.html?identity=1&scenario=collapsed&locale=en&theme=light`);
  await page.evaluate(()=>window.taskCommunication.prepare());
  await page.waitForSelector('.background-task');
  await page.evaluate(()=>window.taskCommunication.tasks(tasks=>{
   tasks[0].phase='complete';tasks[0].started_at_ms=1700000000000;tasks[0].finished_at_ms=1700000138000;
  }));
  await page.locator('.work-record-summary').click();
  await page.locator('[data-call-id="mail-1"]>summary').click();
  await page.locator('[data-call-id="mail-1"] .communication-entry .communication-task-endpoint').filter({hasText:'Layout review'}).first().click();
  await page.waitForSelector('.task-conversation');
  await page.getByRole('button',{name:'Task actions',exact:true}).click();
  await page.getByRole('dialog').getByRole('button',{name:'Task details',exact:true}).click();
  await page.getByRole('dialog').getByText('2m 18s',{exact:true}).waitFor();
  console.log('PASS production Chat → completed Task details: final duration uses authoritative finish');
  await page.close();
 }
 for(const [name,theme,locale,index]of [['desktop-light','light','zh-CN',0],['phone-light','light','zh-CN',1],['narrow-dark','dark','en',2]]){
  for(const version of ['before','after']){
   const page=await browser.newPage({viewport:{width:1280,height:1100},colorScheme:theme,reducedMotion:'reduce',deviceScaleFactor:1});
   await page.goto(`${origin}/scripts/tests/fixtures/background-task-time.html?capture=1${alignment?'&alignment=1':''}&locale=${locale}&theme=${theme}${version==='before'?'&before=1':''}`);
   await page.waitForFunction(()=>window.backgroundTaskTime);
   assert.deepEqual(await page.evaluate(()=>window.backgroundTaskTime.failures),[]);
   if(alignment){
    const aligned=await page.locator('.probe').nth(index).evaluate(host=>{
     const positions=[...host.querySelectorAll('.background-task-status')].map(status=>status.getBoundingClientRect().x);
     return positions.every(x=>Math.abs(x-positions[0])<0.5);
    });
    assert.equal(aligned,version==='after','alignment comparison must reproduce the drift before and shared columns after');
   }
   await page.locator('.probe').nth(index).screenshot({path:`${directory}/background-task-${alignment?'alignment':'time'}-${name}-${version}.png`});
   await page.close();
  }
 }
}finally{await browser.close();}
