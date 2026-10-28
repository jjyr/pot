import assert from 'node:assert/strict';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {assistantCreateHTTPFixture} from './assistant-create-http-fixture.mjs';
const source=await sourceServer(),fixture=await assistantCreateHTTPFixture(source.origin),browser=await launchBrowser();
try{for(const width of [1280,390])for(const locale of ['en','zh-CN']){
 const context=await browser.newContext({viewport:{width,height:900},serviceWorkers:'block'}),page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
 try{
  await page.goto(fixture.origin+'/?locale='+locale+'&theme=light');await page.locator('.assistant-row').first().waitFor();
  const dismiss=page.getByRole('button',{name:locale==='en'?'Not now':'暂不',exact:true});if(await dismiss.isVisible())await dismiss.click();
  await page.locator('.directory-heading button:last-child').click();await page.locator('.assistant-connect-new').click();await page.locator('[data-agent-kind="pot"]').click();
  const url=page.locator('input[type=url]');await url.waitFor();assert.equal(await page.locator('form form').count(),0,'Connection and editor forms cannot nest');
  await url.fill('https://fixture.example/mcp');const form=await url.evaluateHandle(node=>node.form);
  await page.evaluate(()=>{window.outerSubmits=0;for(const form of document.querySelectorAll('form'))if(!form.querySelector('input[type=url]'))form.addEventListener('submit',()=>window.outerSubmits++);window.originalURL=document.querySelector('input[type=url]');});
  const before=fixture.trace.filter(item=>item.method==='POST').length;
  await page.locator('.mcp-connection-form button').click();await page.waitForFunction(()=>!document.querySelector('.mcp-connection-form button')?.disabled);
  assert.equal(await page.evaluate(()=>window.outerSubmits),0,'Connection submit never reaches generic editor save');
  assert(await url.evaluate(node=>node===window.originalURL&&node.isConnected),'Authorization error preserves original editor/input');
  const writes=fixture.trace.filter(item=>item.method==='POST').slice(before);assert.equal(writes.length,1);assert.equal(writes[0].path,'/api/v1/agent-links/mcp');assert.equal(writes[0].body.action,'authorize');
  assert.deepEqual(errors,[]);console.log(JSON.stringify({width,locale,nestedForms:false,outerSave:false,scopePreserved:writes[0].owner}));
 }finally{await context.close();}
}}finally{await browser.close();await fixture.close();await source.close();}
