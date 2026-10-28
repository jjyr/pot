import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {assistantCreateHTTPFixture} from './assistant-create-http-fixture.mjs';

export async function mcpOnboardingBrowser(browser,{sourceOrigin}){
  const fixture=await assistantCreateHTTPFixture(sourceOrigin);
  try{
    const results=[];
    for(const width of [1280,390])for(const locale of ['en','zh-CN'])for(const kind of ['pot','codex','antigravity']){
      const context=await browser.newContext({viewport:{width,height:900},serviceWorkers:'block'}),page=await context.newPage(),errors=[];
      page.on('pageerror',error=>errors.push(error.message));
      try{
        await page.goto(fixture.origin+'/?locale='+locale+'&theme=light');
        await page.locator('.assistant-row').first().waitFor();
        const dismiss=page.getByRole('button',{name:locale==='en'?'Not now':'暂不',exact:true});
        if(await dismiss.isVisible())await dismiss.click();
        const before=fixture.trace.filter(item=>item.method==='POST').length;
        await page.locator('.directory-heading button:last-child').click();
        await page.locator('.assistant-connect-new').click();
        await page.locator('[data-agent-kind="'+kind+'"]').click();
        if(kind==='pot'){
          const url=page.locator('input[type=url]');
          await url.waitFor();
          assert.equal(await page.locator('form form').count(),0,'Connection and editor forms cannot nest');
          await url.fill('https://fixture.example/mcp');
          await page.evaluate(()=>{
            window.outerSubmits=0;
            for(const form of document.querySelectorAll('form'))if(!form.querySelector('input[type=url]'))form.addEventListener('submit',()=>window.outerSubmits++);
            window.originalURL=document.querySelector('input[type=url]');
          });
          await page.locator('.mcp-connection-form button').click();
          await page.waitForFunction(()=>!document.querySelector('.mcp-connection-form button')?.disabled);
          assert.equal(await page.evaluate(()=>window.outerSubmits),0,'Connection submit never reaches generic editor save');
          assert(await url.evaluate(node=>node===window.originalURL&&node.isConnected),'Authorization error preserves original editor/input');
          const writes=fixture.trace.filter(item=>item.method==='POST').slice(before);
          assert.equal(writes.length,1);
          assert.equal(writes[0].path,'/api/v1/agent-links/mcp');
          assert.equal(writes[0].body.action,'authorize');
          assert.deepEqual(errors,[]);
          results.push({width,locale,kind,nestedForms:false,outerSave:false,scopePreserved:writes[0].owner});
        }else{
          await page.locator('.editor-fields a').waitFor();
          assert.equal(
            await page.locator('.editor-fields a').getAttribute('href'),
            'https://github.com/jjyr/pot/blob/main/integrations/'+kind+'/README.md#install-and-connect'
          );
          const hint=page.locator('.editor-fields p.hint');
          assert((await hint.textContent()).trim().length>0,'hint text nonempty');
          assert.equal(await page.locator('input[type=url]').count(),0);
          assert.equal(await page.locator('.agent-connection-setup input').count(),0);
          const after=fixture.trace.filter(item=>item.method==='POST').length;
          assert.equal(after,before,'no POST during setup guidance');
          const box=await page.locator('.editor-fields').boundingBox();
          assert(box&&box.x>=0&&box.x+box.width<=width,'layout fits viewport width');
          assert.deepEqual(errors,[]);
          results.push({width,locale,kind,guideOnly:true,postsUnchanged:true,errorsEmpty:true});
        }
      }finally{
        await context.close();
      }
    }
    return results;
  }finally{
    await fixture.close();
  }
}

if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
  const source=await sourceServer();
  let browser;
  try{
    browser=await launchBrowser();
    const results=await mcpOnboardingBrowser(browser,{sourceOrigin:source.origin});
    console.log(JSON.stringify(results));
  }finally{
    if(browser)await browser.close();
    await source.close();
  }
}