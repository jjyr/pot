import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {attachmentPickerFixture} from './attachment-picker-fixture.mjs';
import {waitForOutbox} from './outbox-persistence-browser.mjs';

const output=resolve(process.env.POT_ISOLATION_OUTPUT||'.pot-artifacts/conversation-isolation');
await mkdir(output,{recursive:true});
const report={passed:false,checks:[],errors:[]};let browser;
try{
  browser=await launchBrowser();
  const versions=[...(process.env.POT_OUTBOX_BEFORE?[['before',process.env.POT_OUTBOX_BEFORE]]:[]),['after',process.env.POT_SOURCE_ROOT]];
  for(const [version,root]of versions){
    const source=await sourceServer({root}),fixture=await attachmentPickerFixture(source.origin,{durableReceipts:true});
    console.log('OWN_SOURCE '+source.origin+' OWN_FIXTURE '+fixture.origin);
    const context=await browser.newContext({viewport:{width:1280,height:800},serviceWorkers:'block'}),page=await context.newPage();
    page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(error.message));
    const state=async()=>(await context.request.get(fixture.origin+'/fixture/state')).json();
    const send=async text=>{await page.locator('#pot-composer').fill(text);await page.locator('.send-button').click();};
    try{
      await page.goto(fixture.origin+'/?locale=en&assistant_id=alpha&conversation=main');
      await page.waitForFunction(()=>document.querySelector('.connection-status.is-connected')&&document.querySelector('.attachment-plus:not(:disabled)'));
      await send('Accepted original conversation');
      await waitForOutbox(page,([rows])=>rows.length===1&&rows[0].state==='accepted'&&!rows[0].projection_pending);
      await context.setOffline(true);await send('Queued original conversation');
      const original=await waitForOutbox(page,([rows])=>rows.length===2&&rows[1].state==='queued');
      await context.request.post(fixture.origin+'/fixture/control',{data:{conversation:'replacement',publish:true}});
      await context.setOffline(false);
      await waitForOutbox(page,([rows])=>rows.length===2&&rows[1].state==='failed'&&rows[1].problem==='conversationRetired');
      const history=page.locator('.workspace > .history');
      const oldVisible=()=>history.locator('article.message:visible').filter({hasText:'original conversation'}).count();
      if(version==='before'){
        await history.locator('article.message:visible').filter({hasText:'Queued original conversation'}).waitFor();
        assert.equal(await oldVisible(),2);report.checks.push('exact555 negative: both accepted optimistic and queued old rows appear in replacement chat');
        await page.screenshot({path:resolve(output,'before.png')});
      }else{
        await page.waitForFunction(()=>![...document.querySelectorAll('.workspace > .history article.message')].some(row=>!row.closest('[data-preparation-surface]')&&getComputedStyle(row).visibility==='visible'&&row.textContent.includes('original conversation')));
        assert.equal(await oldVisible(),0);report.checks.push('complete target isolates queued and accepted optimistic rows from replacement chat');
        await page.screenshot({path:resolve(output,'after.png')});
        await send('New replacement conversation submission');
        const current=await waitForOutbox(page,([rows])=>rows.length===3&&rows[2].target.conversation==='replacement'&&rows[2].state==='accepted'&&!rows[2].projection_pending);
        await history.locator('article.message:visible').filter({hasText:'New replacement conversation submission'}).waitFor();
        assert.equal(current.rows[2].request.conversation_id,'replacement');report.checks.push('replacement conversation can display and deliver its own new submission');
      }
      const server=await state(),posts=server.trace.filter(row=>row.kind==='post');
      assert(!posts.some(row=>row.body.request_id===original.rows[1].request.request_id));
      assert.equal(posts.filter(row=>row.body.request_id===original.rows[0].request.request_id).length,1);
      await page.getByRole('button',{name:'Saved messages',exact:true}).click();
      const retired=page.locator('.outbox-dialog article.message:visible').filter({hasText:'Queued original conversation'});await retired.waitFor();
      assert.match(await retired.innerText(),/conversation/);
      assert.equal(original.rows[1].target.conversation,'main');
      await page.screenshot({path:resolve(output,'saved-'+version+'.png')});
      report.checks.push(version+': retired content and concrete problem remain in Saved messages with zero retargeted POST');
      report[version]={root,rows:original.rows,posts};
    }finally{await context.close();await fixture.close();await source.close();}
  }
  assert.deepEqual(report.errors,[]);report.passed=true;
}catch(error){report.failure=error.stack;throw error;}
finally{await browser?.close();await writeFile(resolve(output,'report.json'),JSON.stringify(report,null,2));}
