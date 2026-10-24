// Actual product DOM with an initially empty, synthetic receiver transport.
// Installation commands are displayed only. Real HTTP/Agent Link is checked separately.
async page=>{
 const state=STATE,zh=state.locale==='zh-CN',checks=[],assert=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
 const cancel=()=>page.locator('.editor-actions button:not(.primary)'),input=()=>page.locator('.agent-connection-setup input'),retry=()=>page.locator('.connection-preview-error button');
 await page.setViewportSize({width:state.width,height:844});
 await page.goto(`${state.origin}/scripts/tests/fixtures/management.html?scenario=first-connection&capture=1&locale=${state.locale}&theme=${zh?'dark':'light'}`);
 await page.waitForFunction(()=>window.assistantStartResults?.passed);
 assert(await page.evaluate(()=>managementFixture.links.length===0&&managementFixture.connections===0),'Starts with zero external connections');
 const open=async()=>{await page.locator('.assistant-connect-new').click();await page.locator('.agent-kind-choice').first().waitFor();};
 await open();assert(await page.locator('.agent-kind-choice').count()===3,'New Agent selector includes Codex, Antigravity and Pot');
 await page.locator('[data-agent-kind=codex]').focus();await page.keyboard.press('Tab');assert(await page.locator('[data-agent-kind=antigravity]').evaluate(node=>node===document.activeElement&&node.matches(':focus-visible')),'Keyboard moves between visible Agent choices');
 const choose=async()=>{await page.locator(`[data-agent-kind=${state.kind}]`).click();await input().waitFor();};
 await choose();assert((await page.locator('.agent-setup-command').innerText()).includes(`integrations/${state.kind}/install.py`),'Guide shows the selected Agent installer');assert((await page.locator('.agent-setup-steps').innerText()).includes('pot-setup'),'Guide explains how to open the receiver connection page');
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Installation guide fits the viewport');
 assert(await page.locator('.agent-connection-setup button').evaluateAll(nodes=>nodes.filter(node=>node.getClientRects().length).every(node=>node.getBoundingClientRect().height>=44)),'Guide actions have 44px minimum targets');
 assert(await page.locator('.agent-setup-steps>li').count()===3,'Installation, invitation and connection are explicit steps');
 await cancel().click();await page.locator('.agent-kind-choice').first().waitFor();await cancel().click();await page.locator('.assistant-connect-new').waitFor();
 assert(await page.evaluate(()=>managementFixture.connectAttempts===0),'Cancelling either step does not connect');
 await open();await choose();await page.evaluate(()=>managementFixture.holdPreview());await input().fill(`tcp://fixture#${state.kind}`);await page.waitForFunction(()=>managementFixture.previewAttempts>0);
 assert(await page.locator('.connection-checking').isVisible(),'Identity verification is visible');
 await cancel().click();await page.getByRole('button',{name:zh?'放弃更改':'Discard changes',exact:true}).click();await page.locator('.agent-kind-choice').first().waitFor();await page.evaluate(()=>managementFixture.releasePreview());
 assert(await page.evaluate(()=>managementFixture.connectAttempts===0),'Cancelling pending verification cannot pair later');
 await choose();const mismatch=state.kind==='codex'?'antigravity':'codex';await input().fill(`tcp://fixture#${mismatch}`);await retry().waitFor();assert(await page.evaluate(()=>managementFixture.connectAttempts===0),'A mismatched Agent invitation is rejected before pairing');
 await page.evaluate(()=>{managementFixture.previewFailure=true;});await input().fill(`tcp://fixture#${state.kind}`);await retry().waitFor();
 await page.evaluate(()=>{managementFixture.connectFailure=true;managementFixture.holdConnect();});await retry().click();await page.waitForFunction(()=>managementFixture.connectAttempts===1);
 assert(await page.locator('.connection-checking').innerText()===(zh?'正在连接…':'Connecting…'),'Pairing has an explicit connecting state');assert(await cancel().isDisabled(),'Accepted pairing waits for its result before leaving');
 await page.evaluate(()=>managementFixture.releaseConnect());await retry().waitFor();
 await page.evaluate(value=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value}})),zh?'en':'zh-CN');
 assert(await input().inputValue()===`tcp://fixture#${state.kind}`,'Locale switching preserves the invitation draft and retry state');
 await page.evaluate(value=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value}})),state.locale);
 await retry().click();await page.locator('.assistant-management-form input').waitFor();
 assert(await page.evaluate(()=>managementFixture.links.length===1&&managementFixture.connections===1&&!managementFixture.scope.owner),'Connection success returns to creation without opening the receiver default');
 assert((await page.locator('.assistant-runtime-summary').innerText()).includes('New '+state.kind),'New connection is selected in the creation summary');
 await page.locator('.assistant-management-form input').fill('First review');await page.locator('.editor-actions .primary').click();await page.waitForFunction(()=>managementFixture.scope.owner.startsWith('agent-link:first-')&&document.querySelector('.pot-app')?.dataset.route==='chat');
 assert(await page.evaluate(()=>managementFixture.scope.owner.endsWith(managementFixture.mutations.at(-1).id)&&!managementFixture.scope.owner.endsWith(':default')),'Only explicit creation opens the newly returned assistant');
 if(state.width<720){await page.locator('.narrow-navigation').click();}
 await page.getByRole('button',{name:zh?'添加助手':'Add assistant',exact:true}).click();await page.locator('[data-runtime-id="connection:first-'+state.kind+'"] .assistant-runtime-choice').click();await page.locator('.assistant-management-form input').fill('Second review');await page.locator('.editor-actions .primary').click();await page.waitForFunction(()=>managementFixture.mutations.length===2&&document.querySelector('.pot-app')?.dataset.route==='chat');
 assert(await page.evaluate(()=>managementFixture.connections===1&&managementFixture.connectAttempts===2),'Creating again on the existing connection does not pair again');
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Flow fits the viewport');assert(await page.evaluate(()=>managementFixture.errors.length===0),'No browser errors');
 return {passed:true,kind:state.kind,locale:state.locale,width:state.width,checks,transport:'synthetic receiver fixture',paidModelCalls:0};
}
