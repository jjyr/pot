// Same synthetic data/view on the archived PR head and the current production source.
import {mkdir} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const [origin='http://127.0.0.1:17723',directory='output/pr-images/task-identity']=process.argv.slice(2);
await mkdir(directory,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
try{
  for(const [name,width,height,theme,scenario]of [
    ['identity-correction-desktop-light-collapsed',1280,1100,'light','collapsed'],
    ['identity-correction-desktop-light-expanded',1280,1900,'light','expanded'],
    ['identity-correction-mobile-dark-collapsed',390,1200,'dark','collapsed'],
    ['identity-correction-narrow-system-collapsed',320,1200,'system','collapsed'],
  ]){
    let sourceData;
    for(const version of ['before','after']){
      const page=await browser.newPage({viewport:{width,height},colorScheme:theme==='system'?'dark':theme,deviceScaleFactor:1,reducedMotion:'reduce'}),errors=[];
      page.on('pageerror',error=>errors.push(error.message));
      const prefix='/'+version;
      await page.goto(`${origin}${prefix}/scripts/tests/fixtures/task-communication.html?identity=1&locale=en&theme=${theme}&scenario=${scenario}`);
      if(width<720)await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
      await page.evaluate(()=>window.taskCommunication.prepare());
      await page.waitForSelector('.background-task');
      if(scenario==='expanded'){await page.locator('.background-tasks-toggle').click();await page.waitForFunction(()=>document.querySelectorAll('.background-task').length===7);await page.evaluate(()=>window.taskCommunication.prepare());}
      await page.evaluate(()=>Promise.all([...document.images].map(image=>image.decode())));
      const data=await page.evaluate(()=>JSON.stringify([window.taskCommunication.messages,window.taskCommunication.catalogue]));
      if(sourceData)assert.equal(data,sourceData,'pair uses identical transcript and catalogue');else sourceData=data;
      assert.deepEqual(errors,[]);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
      assert.match(await page.locator('.communication-stack-title').innerText(),/6 records.*7 messages.*3 tasks/);
      assert.equal(await page.locator('.background-task').count(),scenario==='expanded'?7:3);
      assert.equal(await page.locator('.communication-stack').evaluate(node=>node.open),scenario==='expanded');
      assert.equal(await page.locator('.background-task').last().isVisible(),true);
      await page.screenshot({path:resolve(directory,`${name}-${version}.png`)});
      console.log(`${name}-${version}.png: identical 6 records / 7 messages / 3 communication Tasks / 7 background Tasks`);
      await page.close();
    }
  }
}finally{await browser.close();}
