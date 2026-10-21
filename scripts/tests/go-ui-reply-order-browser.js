// Run with Playwright CLI against a loopback server serving the repository.
async page=>{
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  const check=(value,message)=>{if(!value)throw Error(message);};
  const results=[];
  for(const [locale,width] of [['en',1280],['zh-CN',1280],['zh-CN',393]]){
    await page.setViewportSize({width,height:900});
    await page.goto(new URL('/scripts/tests/fixtures/chat-lifecycle.html',page.url()).href);
    if(width<719)await page.locator('.assistant-row:visible').first().click();
    await page.locator('#pot-composer').waitFor();
    await page.getByRole('button',{name:locale==='en'?'English':'Chinese',exact:true}).click();
    for(let i=1;i<=3;i++){
      await page.locator('#pot-composer').fill(`Silent turn ${i}`);
      await page.locator('#pot-composer').press('Enter');
      await page.locator('article.role-assistant').waitFor();
      await page.getByRole('button',{name:'Empty result',exact:true}).click();
      await page.waitForFunction(count=>document.querySelectorAll('article.role-assistant').length===0&&document.querySelectorAll('article.role-user').length===count,i);
    }
    const checkOrder=async()=>{
      const rows=await page.locator('article.message').evaluateAll(nodes=>nodes.map(node=>({id:node.dataset.messageId,role:node.classList.contains('role-user')?'user':'assistant'})));
      check(rows.length===5,'Four inputs and one agent-authored reply leave no empty outcome rows');
      for(let i=0;i<4;i++)check(rows[i].role==='user','Inputs remain in chronological order after silent completion');
      check(rows[4].role==='assistant'&&!rows[4].id.startsWith('waiting:'),'Only the actual reply creates a completed assistant bubble');
      check(await page.locator('.history').evaluate(node=>node.scrollHeight-node.clientHeight-node.scrollTop<3),'Chat keeps following the latest message');
    };
    await page.locator('#pot-composer').fill('New message after three silent turns');
    await page.locator('#pot-composer').press('Enter');
    await page.getByRole('button',{name:'Reply',exact:true}).click();
    await page.getByText('The reply arrived.',{exact:true}).waitFor();
    await page.waitForTimeout(100);
    await checkOrder();
    // A fresh subscription/reset must reconstruct the same chronological rows.
    await page.getByRole('button',{name:'Disconnected',exact:true}).click();
    await page.getByRole('button',{name:'Reconnected',exact:true}).click();
    await page.waitForTimeout(100);await checkOrder();
    const oldReply=page.getByText('The reply arrived.',{exact:true});
    const oldNode=await oldReply.locator('xpath=ancestor::article').elementHandle();
    await page.getByRole('button',{name:locale==='en'?'Chinese':'English',exact:true}).click();
    check(await oldNode.evaluate(node=>node.isConnected),'Live locale switching retains the outcome node');
    await page.getByRole('button',{name:locale==='en'?'English':'Chinese',exact:true}).click();
    await page.waitForTimeout(100);await checkOrder();
    await page.locator('aside[aria-label="Fixture controls"]').evaluate(node=>node.hidden=true);
    await page.screenshot({path:`output/playwright/reply-order/chat-${width}-${locale}-after.png`});
    results.push({locale,width,ordered:true,silentCompletion:true,reconnected:true});
  }
  check(!errors.length,'No browser errors: '+errors.join('; '));
  return results;
}
