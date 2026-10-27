// Independent capture browser; production components, synthetic originals, no backend writes.
import assert from 'node:assert/strict';
import {mkdir,readFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const [origin='http://127.0.0.1:17741',before='',directory='output/pr-images/native-image-preview']=process.argv.slice(2);
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
await mkdir(directory,{recursive:true});
const fixture='/scripts/tests/fixtures/image-viewer.html';
async function ready(page,path=fixture){await page.goto(origin+path);await page.waitForFunction(()=>window.imageViewerFixture?.ready);}
async function settled(page){await page.locator('dialog[open]').evaluate(async dialog=>{await Promise.all(dialog.getAnimations().map(animation=>animation.finished));});}
try{
  for(const [width,locale,theme]of [[1280,'en','light'],[1280,'zh-CN','dark'],[390,'en','light'],[390,'zh-CN','dark'],[320,'zh-CN','dark']]){
    const page=await browser.newPage({viewport:{width,height:800},colorScheme:theme,reducedMotion:'reduce'}),errors=[];page.on('pageerror',error=>errors.push(error.message));
    try{
      await ready(page,`${fixture}?locale=${locale}&theme=${theme}`);
      assert.equal((await page.evaluate(()=>window.verifyImageViewer())).passed,true);
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'No horizontal page overflow');
      const trigger=page.locator('#resource .resource-image');await trigger.scrollIntoViewIfNeeded();
      await page.evaluate(()=>{const host=document.querySelector('#resource');host.style.cssText='height:450px;overflow:auto';host.scrollTop=70;});
      const position=await page.evaluate(()=>({page:scrollY,chat:document.querySelector('#resource').scrollTop}));
      await trigger.evaluate(element=>element.focus({preventScroll:true}));await page.keyboard.press('Enter');await settled(page);
      assert.deepEqual(await page.evaluate(()=>({page:scrollY,chat:document.querySelector('#resource').scrollTop})),position,'Opening keeps the current chat/page scroll');
      const view=page.locator('.image-viewer-viewport');
      const rect=await view.boundingBox();
      await page.mouse.move(rect.x+rect.width*.65,rect.y+rect.height*.65);await page.mouse.wheel(0,360);
      await page.waitForFunction(()=>document.querySelector('.image-viewer-viewport').scrollTop>100);
      await view.evaluate(element=>element.scrollTop=80);
      const top=await view.evaluate(element=>element.scrollTop);
      await page.mouse.move(rect.x+rect.width*.65,rect.y+rect.height*.65);await page.mouse.down();await page.mouse.move(rect.x+rect.width*.45,rect.y+rect.height*.35,{steps:8});await page.mouse.up();
      assert(await view.evaluate(element=>element.scrollTop)>top+80,'Mouse drag pans vertically');
      await page.locator('.image-viewer-zoom-in').click();
      await page.mouse.move(rect.x+rect.width*.6,rect.y+rect.height*.5);await page.mouse.down();await page.mouse.move(rect.x+rect.width*.4,rect.y+rect.height*.5,{steps:8});await page.mouse.up();
      assert(await view.evaluate(element=>element.scrollLeft)>10,'Mouse drag pans horizontally after zoom');
      const scale=await page.locator('dialog output').textContent();
      await page.keyboard.down('Control');await page.mouse.wheel(0,-180);await page.keyboard.up('Control');
      await page.waitForFunction(value=>document.querySelector('dialog output').textContent!==value,scale);
      await view.evaluate(element=>element.scrollTop=element.scrollHeight);await page.mouse.wheel(0,1000);
      // Wait for native wheel delivery before checking that it cannot chain to chat.
      await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
      assert.deepEqual(await page.evaluate(()=>({page:scrollY,chat:document.querySelector('#resource').scrollTop})),position,'Wheel at image boundary cannot scroll chat/page');
      await view.click({position:{x:20,y:20}});assert.equal(await page.locator('dialog[open]').count(),1,'Image click does not dismiss');
      // Tab cycles inside the native modal, including its scrollable keyboard surface.
      for(let i=0;i<9;i++){await page.keyboard.press('Tab');assert(await page.evaluate(()=>document.activeElement.closest('dialog')!==null),'Native dialog traps keyboard focus');}
      for(let i=0;i<9;i++){await page.keyboard.press('Shift+Tab');assert(await page.evaluate(()=>document.activeElement.closest('dialog')!==null),'Reverse Tab stays in modal');}
      await page.keyboard.press('Escape');assert.equal(await page.locator('dialog').count(),0,'Escape removes the dialog');
      assert.equal(await trigger.evaluate(element=>document.activeElement===element),true,'Escape restores image focus');
      assert.deepEqual(await page.evaluate(()=>({page:scrollY,chat:document.querySelector('#resource').scrollTop})),position,'Escape preserves chat and page scroll');
      await page.keyboard.press('Space');await settled(page);await page.locator('.file-image-close').click();assert.equal(await page.locator('dialog').count(),0,'Close button works after reopening');
      assert.deepEqual(errors,[]);console.log(`PASS image preview ${width}px ${locale} ${theme}: component suite, native wheel, mouse pan, Ctrl+wheel, focus trap, Escape, reopen`);
    }finally{await page.close();}
  }
  // Existing artifact browser regression uses production adapters with synthetic reads.
  const page=await browser.newPage();
  try{
    await page.route('**/*',async route=>{
      const path=new URL(route.request().url()).pathname;
      if(!/^\/(components|state|adapters|locales|vendor|styles|assets)\//.test(path)&&path!=='/styles.css')return route.continue();
      try{const body=await readFile(resolve('web/src'+path));await route.fulfill({body,contentType:path.endsWith('.css')?'text/css':path.endsWith('.json')?'application/json':path.endsWith('.mjs')||path.endsWith('.js')?'application/javascript':'application/octet-stream'});}catch{await route.continue();}
    });
    await page.goto(origin);
    const source=await readFile(new URL('./pot-ui-file-images-browser.js',import.meta.url),'utf8'),run=new Function(`return (${source}\n)`)();
    assert.equal((await run(page)).passed,true);console.log('PASS existing artifact image browser regression: owner scope, lease, streaming, download, tool, Task, locale, failure');
  }finally{await page.close();}
  for(const [name,width,height,locale,theme,scenario]of [
    ['inline-desktop-light',1280,800,'en','light','inline'],
    ['preview-desktop-light',1280,800,'en','light','preview'],
    ['preview-mobile-dark',390,800,'zh-CN','dark','preview'],
  ])for(const [version,prefix]of [...(before?[['before',before]]:[]),['after','']]){
    const page=await browser.newPage({viewport:{width,height},colorScheme:theme,reducedMotion:'reduce'});
    try{
      await ready(page,`${prefix}${fixture}?locale=${locale}&theme=${theme}`);
      if(scenario==='preview'){
        await page.locator('#markdown .file-image-preview').click();await settled(page);
        await page.locator('dialog img').evaluate(image=>image.decode());
      }
      await page.screenshot({path:resolve(directory,`${name}-${version}.png`)});console.log(`Captured ${name}-${version}`);
    }finally{await page.close();}
  }
}finally{await browser.close();}
