// Serve the checkout root; use an external Playwright installation, as for website-browser.mjs.
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
const browser=await chromium.launch({headless:true,executablePath:process.env.POT_CHROMIUM_EXECUTABLE});
try {
 const origin=process.env.POT_WEBSITE_ORIGIN||'http://127.0.0.1:4173';
 if(process.argv.includes('--website-only')){
  for(const [view,width,height,file]of [
   ['hero',720,560,'product-hero'],['hero',390,600,'product-work-mobile'],
   ['work',1120,620,'product-work'],['assistants',920,440,'product-assistants'],['assistants',390,590,'product-assistants-mobile'],['link',800,510,'product-link'],['link',390,540,'product-link-mobile'],
  ]){
   const page=await browser.newPage({viewport:{width,height},reducedMotion:'reduce'}),errors=[],unexpected=[];
   page.on('pageerror',error=>errors.push(error.message));
   await page.route('**/*',route=>{const request=route.request();if(request.method()!=='GET'||new URL(request.url()).origin!==origin){unexpected.push(request.url());return route.abort();}return route.continue();});
   await page.goto(`${origin}/scripts/tests/fixtures/website-product.html?view=${view}`);
   await page.waitForFunction(()=>window.websiteProduct?.ready);
   if(view==='work')await page.locator('pot-resource[data-state="ready"]').waitFor();
   const observed=await page.evaluate(()=>({errors:websiteProduct.errors,mutations:websiteProduct.mutations,
    identities:[...document.querySelectorAll('.background-task')].map(row=>({short:row.querySelector('.task-identity-short').textContent,color:Number(row.querySelector('.task-identity-mark').dataset.taskColor)})),
    expected:websiteProduct.expected,overflow:document.documentElement.scrollWidth>innerWidth,
    states:[...document.querySelectorAll('.background-task-status')].map(el=>el.getBoundingClientRect().x),
    clocks:[...document.querySelectorAll('.background-task-elapsed')].map(el=>el.getBoundingClientRect().x),
   }));
   assert.deepEqual(observed.errors,[]);assert.deepEqual(observed.mutations,[]);assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[]);assert.equal(observed.overflow,false);
   if(view==='hero'||view==='work'){
    assert.deepEqual(observed.identities,observed.expected);
    assert(observed.states.every(x=>Math.abs(x-observed.states[0])<1));assert(observed.clocks.every(x=>Math.abs(x-observed.clocks[0])<1));
    assert(observed.clocks[0]>observed.states[0]+35);
   }
   if(view==='assistants'){assert(await page.locator('.plate-directory').isVisible());assert.equal(await page.locator('.assistant-entry .avatar>svg').count(),2,'Missing profile images use generic icons');}
   await page.locator('#plate').screenshot({path:`website/assets/${file}.png`});
   console.log(`${file}: production components; authoritative fixture identities; no transport or mutations`);await page.close();
  }
 }else{
 for(const [view,file,count] of [['work','conversation-tasks',3],['link','agent-link',1]]){
  const page=await browser.newPage({viewport:{width:1280,height:900},reducedMotion:'reduce'});
  const errors=[],unexpected=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/*',route=>{
   const request=route.request();
   if(request.method()!=='GET'||new URL(request.url()).origin!==origin){unexpected.push(request.url());return route.abort();}
   return route.continue();
  });
  await page.goto(`${origin}/scripts/tests/fixtures/product-overview.html?view=${view}`);
  const development=page.locator('.assistant-row[data-search-name="development"]');
  await development.waitFor();
  await page.waitForFunction(()=>{const image=document.querySelector('.assistant-row[data-search-name="development"] .avatar-image');return image?.complete&&image.naturalWidth===128&&image.naturalHeight===128;});
  assert.equal(await development.locator('.avatar-image').count(),1);
  await page.waitForFunction(expected=>document.querySelectorAll('.background-task').length===expected,count);
  assert.equal(await page.locator('.background-task').count(),count);
  assert.equal(await page.locator('.background-task .task-identity-mark[data-task-color]').count(),count);
  for(const label of await page.locator('.background-task .task-identity-short').allTextContents())assert.match(label,/^#[0-9a-f]{4}$/);
  assert.equal(await page.locator('.background-task-elapsed').count(),count);
  await page.getByText(view==='work'?'The first results are ready.':'Codex returned the documentation review.',{exact:true}).waitFor();
  await page.evaluate(()=>document.fonts.ready);
  assert.deepEqual(await page.evaluate(()=>({errors:productOverview.errors,mutations:productOverview.mutations})),{errors:[],mutations:[]});
  assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[]);
  await page.screenshot({path:`docs/images/${file}.png`});
  console.log(`${view}: saved default avatar loaded; read-only fixture and ${count} task identities, states and clocks verified; screenshots refreshed`);
  await page.close();
 }
 }
} finally {await browser.close();}
