// Equal synthetic records and viewport in two archived production source trees.
import {mkdir} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const [origin='http://127.0.0.1:17723',directory='output/pr-images/task-communication']=process.argv.slice(2);
await mkdir(directory,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
try{
  for(const [name,width,height,theme,scenario]of [
    ['chat-desktop-light-collapsed',1280,1000,'light','collapsed'],
    ['chat-desktop-light-expanded',1280,1400,'light','expanded'],
    ['chat-mobile-dark-expanded',390,1400,'dark','expanded'],
  ])for(const version of ['before','after']){
    const page=await browser.newPage({viewport:{width,height},colorScheme:theme,deviceScaleFactor:1,reducedMotion:'reduce'}),errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    await page.goto(`${origin}/${version}/scripts/tests/fixtures/task-communication.html?locale=en&theme=${theme}&scenario=${scenario}`);
    if(width<720)await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
    await page.evaluate(()=>window.taskCommunication.prepare());
    await page.evaluate(()=>Promise.all([...document.images].map(image=>image.decode())));
    assert.deepEqual(errors,[]);
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    assert.equal(await page.evaluate(()=>window.taskCommunication.messages.length),8);
    if(version==='after'){
      assert.match(await page.locator('.communication-stack-title').innerText(),/6 records.*7 messages.*3 tasks/);
      assert.equal(await page.locator('.communication-stack').evaluate(node=>node.open),scenario!=='collapsed');
      if(scenario!=='collapsed')assert.equal(await page.locator('.communication-entry').count(),7);
    }else assert.equal(await page.locator('.communication-tool').count(),6);
    await page.screenshot({path:resolve(directory,`${name}-${version}.png`)});
    console.log(`${name}-${version}.png: production source, equal 6 records / 7 messages / 3 Tasks`);
    await page.close();
  }
}finally{await browser.close();}
