import assert from 'node:assert/strict';
import {preparedNativeFocusIntent} from './prepared-focus-browser.mjs';
import {preparedNativeInputReady,preparedNativeUnfocusedTab,preparedNativePresentationFences} from './prepared-native-input-browser.mjs';
import {preparedNativeResizeBudget} from './prepared-native-budget-browser.mjs';

export async function preparedNativeLifecycle(browser,origin){
  const results=[];
  for(const query of ['', '?cancel', '?cancel-back', '?fallback&cancel&keyboard']){
    const context=await browser.newContext(),page=await context.newPage(),errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    try{
      await page.goto(origin+'/scripts/tests/fixtures/prepared-native.html'+query);
      if(query.includes('keyboard')){
        await page.waitForFunction(()=>window.nativeKeyboardReady);
        // Send to the current focus without refocusing the iframe element;
        // refocusing the host can itself reset a sandbox's internal focus.
        await page.keyboard.press('Enter');
      }
      await page.waitForFunction(()=>window.preparedNativeResult);
      const result=await page.evaluate(()=>window.preparedNativeResult);
      assert.equal(result.failure,null);assert.deepEqual(errors,[]);
      assert.deepEqual(result.errors,[]);assert.equal(result.acquired,result.released);
      assert.equal(result.observations.find(item=>item.label==='fresh-current-width-reference').parity,0);
      results.push({query,...result});
    }finally{await context.close();}
  }
  for(const scenario of ['commit','prepare','prepare-raf']){
    const context=await browser.newContext(),page=await context.newPage();
    try{
      await page.goto(origin+'/scripts/tests/fixtures/prepared-failure.html?scenario='+scenario);
      await page.waitForFunction(()=>window.preparedFailureResult);
      const result=await page.evaluate(()=>window.preparedFailureResult);
      assert.equal(result.failure,null);assert.equal(result.errors.length,1);
      assert(result.errors[0].message.includes('Synthetic independent '+(scenario==='commit'?'commit':'preparation')+' interruption'));
      assert.equal(result.acquired,result.released);
      results.push(result);
    }finally{await context.close();}
  }
  const context=await browser.newContext(),page=await context.newPage(),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
    await page.goto(origin+'/scripts/tests/fixtures/prepared-events.html');
    await page.waitForFunction(()=>window.htmlEvents?.ready);
    const document=page.frameLocator('.history:not([data-preparation-surface]) iframe');
    // Keep this rapid sequence: per-action ACK/RAF waits hide native input
    // surfaces retired unnecessarily by outer result/notice preparation.
    await document.getByRole('button',{name:'Grow content'}).click();
    await document.getByRole('button',{name:'Publish result'}).click();
    await document.getByRole('button',{name:'Report dependency'}).click();
    await page.getByRole('button',{name:'Finish iframe actions'}).click();
    await page.waitForFunction(()=>window.preparedEventsResult);
    const result=await page.evaluate(()=>window.preparedEventsResult);
    assert.equal(result.failure,null);assert.deepEqual(errors,[]);assert.deepEqual(result.errors,[]);
    assert.deepEqual(result.actions.map(item=>item.action),['grow','result','dependency']);
    assert(result.actions.every(item=>item.live&&item.trusted&&item.token===result.actions[0].token&&item.documentID===result.actions[0].documentID));
    assert.equal(result.stagedAfterResult,null);assert.equal(result.loads,0);
    assert.equal(result.acquired,result.released);results.push(result);
  }finally{await context.close();}
  results.push({focusIntent:await preparedNativeFocusIntent(browser,origin)});
  results.push({inputReady:await preparedNativeInputReady(browser,origin)});
  results.push({unfocusedTab:await preparedNativeUnfocusedTab(browser,origin)});
  results.push({presentationFences:await preparedNativePresentationFences(browser,origin)});
  results.push({resizeBudget:await preparedNativeResizeBudget(browser,origin)});
  return results;
}
