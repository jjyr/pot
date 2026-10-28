import assert from 'node:assert/strict';

export async function preparedNativeFocusIntent(browser,origin){
  const results=[];
  for(const scenario of ['keyboard-commit','tab-commit','tab-resize-cancel','tab-owner-retire','click-commit','click-resize-cancel','click-owner-retire']){
    const context=await browser.newContext({viewport:{width:1280,height:900}}),page=await context.newPage(),errors=[],checks=[];
    page.on('pageerror',error=>errors.push(error.message));
    try{
      await page.goto(origin+'/scripts/tests/fixtures/prepared-focus.html');
      await page.waitForFunction(()=>nativeCase.ready);
      const iframe=page.frameLocator('.history:not([data-preparation-surface]) iframe');
      await iframe.getByRole('textbox',{name:'State value'}).click();
      await page.keyboard.type('retained trusted state');await page.keyboard.press('Enter');
      const probe=async label=>{
        await page.evaluate(label=>nativeCase.probe(label),label);
        await page.waitForFunction(label=>nativeCase.messages.some(message=>message.label===label),label);
        return page.evaluate(label=>nativeCase.messages.findLast(message=>message.label===label),label);
      };
      const before=await probe('before');checks.push(before);
      assert(before.outerFocus&&before.internalFocus==='state'&&before.state===18);
      assert.equal(before.value,'retained trusted state');
      await page.evaluate(()=>nativeCase.start());await page.waitForFunction(()=>nativeCase.staged);
      const staged=await page.evaluate(()=>nativeCase.snapshot('staged'));checks.push(staged);
      assert(staged.staged&&staged.allSurfacesInert&&!staged.hiddenFocus);
      const during=await probe('during');checks.push(during);
      assert.equal(during.documentID,before.documentID);assert.equal(during.token,before.token);
      assert(during.outerFocus&&during.internalFocus==='state');
      await page.keyboard.press('Enter');
      await page.waitForFunction(()=>nativeCase.messages.some(message=>message.label==='keydown'&&message.state===19));
      const key=await page.evaluate(()=>nativeCase.messages.findLast(message=>message.label==='keydown'));checks.push(key);
      assert(key.trusted&&key.outerFocus&&key.internalFocus==='state');
      if(scenario.startsWith('tab-')){
        for(let index=0;index<3;index++){
          await page.keyboard.press('Tab');checks.push(await page.evaluate(index=>nativeCase.snapshot('trusted-tab-'+index),index));
        }
      }else if(scenario.startsWith('click-'))await page.getByRole('button',{name:'After live',exact:true}).click();
      const leaving=scenario!=='keyboard-commit';
      if(leaving)assert.equal(await page.evaluate(()=>document.activeElement.id),'after');
      if(scenario.endsWith('resize-cancel')){
        await page.evaluate(()=>nativeCase.resize());checks.push(await page.evaluate(()=>nativeCase.snapshot('after-resize-cancel')));
        assert.equal(await page.evaluate(()=>document.activeElement.id),'after','Cancellation cannot restore a retired focus snapshot');
      }
      let detail;
      if(scenario.endsWith('owner-retire')){
        detail=await page.evaluate(()=>nativeCase.close());
        assert.equal(await page.evaluate(()=>document.activeElement.id),'after','Owner retirement must retain the latest external focus');
      }else{
        await page.evaluate(()=>nativeCase.release());await page.waitForFunction(()=>nativeCase.ready);
        const after=await probe('after');checks.push(after);
        assert.equal(after.documentID,before.documentID);assert.equal(after.token,before.token);
        assert.equal(after.value,before.value);assert.equal(after.state,19);
        assert(await page.evaluate(()=>document.querySelector('.history:not([data-preparation-surface]) iframe')===window.originalNative));
        if(leaving)assert.equal(await page.evaluate(()=>document.activeElement.id),'after','Commit must retain the latest trusted focus');
        else assert(after.outerFocus&&after.internalFocus==='state');
        detail=await page.evaluate(()=>nativeCase.close());
        if(leaving)assert.equal(await page.evaluate(()=>document.activeElement.id),'after');
      }
      assert.equal(detail.loads,0);assert.equal(detail.acquired,detail.released);
      assert(detail.frames.every(frame=>!frame.hiddenFocus&&frame.allSurfacesInert));
      assert.deepEqual(detail.errors,[]);assert.deepEqual(errors,[]);
      results.push({scenario,checks,acquired:detail.acquired,released:detail.released,loads:detail.loads});
    }finally{await context.close();}
  }
  return results;
}
