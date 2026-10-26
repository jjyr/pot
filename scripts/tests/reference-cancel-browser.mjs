import assert from 'node:assert/strict';

// Exercise the shared production UI, including IndexedDB view restoration. Only
// the client boundary is synthetic; sends are captured and never reach a model.
export async function referenceCancellation(page,origin){
  const phone=page.viewportSize().width<600;
  await page.goto(origin+'/scripts/tests/fixtures/reference-cancel.html?conversation=alpha&locale='+(phone?'zh-CN':'en'));
  await page.waitForSelector('[data-reference-source]');
  const input=page.locator('.composer-input'),remove=page.locator('.composer-references .message-reference-remove');
  const state=()=>page.evaluate(()=>({cards:document.querySelectorAll('.composer-references .message-reference').length,selection:getSelection().toString(),text:document.querySelector('.composer-input').value,attachments:document.querySelectorAll('.composer-attachments .attachment-card').length}));
  const select=()=>page.evaluate(()=>referenceFixture.select());
  const cancel=async()=>{assert.equal(await remove.isEnabled(),true,'Draft removal remains available during acceptance');if(phone)await remove.tap();else await remove.click();};
  await input.fill('Keep text and attachment');
  await page.locator('.attachment-plus').click();
  const chooser=page.waitForEvent('filechooser');
  await page.getByRole('menuitem',{name:'PDF',exact:true}).click();
  await (await chooser).setFiles({name:'synthetic.pdf',mimeType:'application/pdf',buffer:Buffer.from('%PDF-1.4 synthetic fixture')});
  await page.waitForSelector('.attachment-card');
  await select();await cancel();
  assert.deepEqual(await state(),{cards:0,selection:'',text:'Keep text and attachment',attachments:1},'Idle cancellation releases selection and preserves other draft content');
  await page.keyboard.press('ArrowDown');assert.equal((await state()).cards,0,'Keyup cannot recapture a cancelled selection');
  await select();await page.locator('.send-button').click();
  await page.waitForFunction(()=>referenceFixture.calls.length===1);
  await input.fill('Next input survives the late receipt');
  await cancel();
  assert.deepEqual(await state(),{cards:0,selection:'',text:'Next input survives the late receipt',attachments:1},'Pending cancellation changes only the editable reference draft');
  const sent=await page.evaluate(()=>referenceFixture.calls[0]);
  assert.equal(sent.request.references.length,1,'An already submitted request retains its original references');
  assert.equal(sent.request.attachments.length,1);
  // Switch owners before the old send settles; its late acknowledgement must
  // preserve Beta's selection and Alpha's explicit cancellation.
  const choose=async name=>{
    if(phone)await page.locator('.narrow-navigation').click();
    await page.locator('.assistant-row').filter({hasText:name}).click();
    await page.waitForFunction(owner=>document.querySelector('[data-reference-source]')?.dataset.referenceSource===owner+'-source',name.toLowerCase());
  };
  await choose('Beta');await input.fill('Beta draft');await select();
  await page.evaluate(()=>referenceFixture.accept());
  await page.waitForTimeout(50);
  assert.equal((await state()).cards,1,'Late Alpha acknowledgement does not clear Beta references');
  assert.equal((await state()).text,'Beta draft');
  await choose('Alpha');
  assert.deepEqual(await state(),{cards:0,selection:'',text:'Next input survives the late receipt',attachments:0},'Returning restores the cancelled draft; only submitted attachments were acknowledged');
  await select();await cancel();
  await page.locator('.send-button').click();await page.waitForFunction(()=>referenceFixture.calls.length===2);
  const next=await page.evaluate(()=>referenceFixture.calls[1].request);
  assert.equal(next.text,'Next input survives the late receipt');
  assert.equal('references' in next,false,'The next payload omits cancelled references');
  assert.equal('reply_to' in next,false);
  assert.equal('user_message_id' in next,false);
  await page.evaluate(()=>referenceFixture.accept());
  await page.waitForFunction(()=>document.querySelector('.composer-input').value==='');
  await page.reload();await page.waitForSelector('[data-reference-source]');
  assert.equal((await state()).cards,0,'Restart does not resurrect in-memory reference drafts');
  return {viewport:page.viewportSize(),idleCancel:true,pendingCancel:true,lateOwnerReceipt:true,reloadClear:true,nextPayload:next};
}
