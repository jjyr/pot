// Real shared editor and captured-scope lifecycle, with synthetic owner API facts.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[];page.on('pageerror',error=>errors.push(error.message));
 const cdp=await page.context().newCDPSession(page);await cdp.send('Network.setCacheDisabled',{cacheDisabled:true});
 await page.setViewportSize({width:1024,height:700});await page.goto(new URL('/scripts/tests/fixtures/topology.html',page.url()).href);
 await page.locator('#fixture-scenario').selectOption('neutral');
 await page.getByRole('button',{name:'My Mac · Execution machine · Connected',exact:true}).click();await page.getByRole('button',{name:'Join a Control',exact:true}).click();
 const info={version:1,url:'tcp://cloud.example:8793',control_id:'cloud-id',fingerprint:'SHA256:fixture-fingerprint-cloud',invitation:'synthetic-private-invitation'},input=page.getByLabel('Join information',{exact:true}),submit=page.locator('.editor-actions .primary');
 await page.evaluate(()=>topologyFixture.wrongControl=true);await input.fill(JSON.stringify(info));
 await page.getByText('This address does not match the Control in your invitation. Check the address with its owner.',{exact:true}).waitFor();
 assert(await submit.isDisabled(),'Mismatched Control cannot join');
 await page.evaluate(()=>topologyFixture.wrongControl=false);await input.fill(info.url);await page.getByText('Manual confirmation required',{exact:true}).waitFor();
 assert(await submit.isDisabled(),'Bare addresses require explicit confirmation');
 await page.getByLabel('Join invitation',{exact:true}).fill(info.invitation);await page.getByLabel('I verified this Runtime ID and fingerprint',{exact:true}).check();assert(await submit.isEnabled(),'Manual path is available after owner verification');
 await input.fill(JSON.stringify(info));await page.getByText('Identity matches invitation',{exact:true}).waitFor();
 assert(!await page.getByLabel('I verified this Runtime ID and fingerprint',{exact:true}).isVisible(),'Full information avoids redundant manual confirmation');
 assert(await input.inputValue()===info.url,'Raw invitation JSON is removed from the input');
 await page.setViewportSize({width:393,height:700});
 assert(await input.evaluate(element=>element.clientWidth)>150,'Mobile address input remains usable');
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Runner editor fits mobile');
 await page.locator('#fixture-language').selectOption('zh-CN');await page.getByText('身份与邀请一致',{exact:true}).waitFor();
 await page.evaluate(()=>{topologyFixture.joinWait=true;topologyFixture.clipboardReadWait=true;});
 await page.getByRole('button',{name:'粘贴',exact:true}).click();await page.waitForFunction(()=>!!topologyFixture.releaseClipboardRead);
 await submit.click();await page.waitForFunction(()=>!!topologyFixture.releaseJoin);
 assert(await page.getByRole('button',{name:'粘贴',exact:true}).isDisabled(),'Join pending disables custom clipboard action');
 await page.evaluate(()=>topologyFixture.releaseClipboardRead('{invalid}'));
 await page.evaluate(()=>topologyFixture.releaseJoin());await page.getByRole('heading',{name:'拓扑',exact:true}).waitFor();
 const joins=await page.evaluate(()=>topologyFixture.calls.filter(call=>call.body?.action==='join'));
 assert(joins.length===1&&joins[0].space==='local'&&joins[0].body.request.expected_runtime_id===info.control_id&&joins[0].body.request.expected_fingerprint===info.fingerprint,'Late clipboard read cannot alter acknowledged membership or its captured owner');
 assert(!errors.length,'No browser exceptions: '+errors.join('; '));
 return {passed:true,automaticIdentityMatch:true,mismatchRejected:true,manualFallback:true,localeAndMobile:true,pendingClipboardFence:true};
}
