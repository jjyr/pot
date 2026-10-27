// Serve the checkout root; use an external Playwright installation, as for website-browser.mjs.
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE||'playwright');
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
const browser=await chromium.launch({headless:true,executablePath:process.env.POT_CHROMIUM_EXECUTABLE});
try {
 const origin=process.env.POT_WEBSITE_ORIGIN||'http://127.0.0.1:4173';
 for(const [view,file,copy,count] of [['work','conversation-tasks','product-work',3],['link','agent-link','product-agent-link',1]]){
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
  if(view==='work'){
   // Crop the actual app, keeping goal, results, identities and status readable.
   await page.screenshot({path:'website/assets/product-work.png',clip:{x:290,y:0,width:950,height:790}});
   await page.screenshot({path:'website/assets/product-assistants.png',clip:{x:55,y:0,width:1000,height:460}});
   await page.setViewportSize({width:390,height:760});
   await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
   await page.locator('.background-task').first().waitFor();
   await page.screenshot({path:'website/assets/product-work-mobile.png'});
  }else await fs.copyFile(`docs/images/${file}.png`,`website/assets/${copy}.png`);
  console.log(`${view}: saved default avatar loaded; read-only fixture and ${count} task identities, states and clocks verified; screenshots refreshed`);
  await page.close();
 }
} finally {await browser.close();}
