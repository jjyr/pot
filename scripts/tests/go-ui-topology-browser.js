// Serve the repository root and run this expression on that disposable origin.
// All API facts come from fixtures/topology.mjs; no real connection is created.
async page => {
 const assert=(condition,message)=>{if(!condition)throw Error(message);};
 await page.goto(new URL('/scripts/tests/fixtures/topology.html',page.url()).href);
 await page.getByText('3 Agents · 4 machines',{exact:true}).waitFor();
 assert(await page.locator('.topology-node.machine').count()===4,'Only actual inventory creates machine nodes');
 assert(await page.locator('.machine-permissions select').count()===0,'Hidden machines do not eagerly render every permission form');
 await page.getByRole('button',{name:'My Mac · Execution machine · Connected',exact:true}).click();
 assert(await page.locator('.topology-inspector .machine-permissions select').count()===1,'Selecting a machine loads its existing execution policy controls');
 await page.locator('.topology-node').filter({hasText:'Codex'}).click();
 assert(await page.locator('.topology-inspector').getByRole('heading',{name:'Codex',exact:true}).count()===1,'Click opens Codex without a fictional machine wrapper');
 assert(!await page.locator('[data-tooltip]').count(),'No rejected hover-information layer');
 await page.getByRole('button',{name:'Pause animation',exact:true}).click();
 assert(await page.locator('.topology-map').evaluate(element=>element.classList.contains('topology-paused')),'Ready animation can be paused');
 await page.getByRole('button',{name:'Add Agent',exact:true}).click();
 await page.getByLabel('Other Agent address',{exact:true}).fill('https://second.example/connect#synthetic');
 await page.locator('#fixture-language').selectOption('zh-CN');
 assert(await page.getByLabel('对方连接地址',{exact:true}).inputValue()==='https://second.example/connect#synthetic','Locale changes preserve sensitive memory-only input');
 await page.locator('#fixture-scenario').selectOption('expired');await page.getByRole('button',{name:'连接',exact:true}).click();
 await page.getByText('连接地址已失效，请对方重新复制。',{exact:true}).waitFor();
 assert(await page.getByLabel('对方连接地址',{exact:true}).inputValue().then(value=>value.includes('#synthetic')),'Rejected connection retains its draft');
 await page.locator('#fixture-scenario').selectOption('ready');await page.getByRole('button',{name:'连接',exact:true}).click();
 await page.locator('.topology-inspector').getByRole('heading',{name:'Second Codex',exact:true}).waitFor();
 assert(await page.locator('.topology-node.machine').count()===4,'New Agent does not change machine count');
 assert(await page.locator('.topology-map').evaluate(element=>element.classList.contains('topology-paused')),'Animation choice survives editing and refresh');
 await page.locator('#fixture-language').selectOption('en');
 await page.getByRole('button',{name:'Add machine',exact:true}).click();
 await page.getByLabel('Owning Control',{exact:true}).selectOption('local');
 await page.getByText('The embedded Control cannot invite remote machines. Use an authorized standalone Control.',{exact:true}).waitFor();
 assert(!await page.getByRole('button',{name:'Create join invitation',exact:true}).isEnabled(),'Embedded Control cannot sign remote membership');
 await page.getByLabel('Owning Control',{exact:true}).selectOption('cloud');await page.getByRole('button',{name:'Create join invitation',exact:true}).click();
 await page.getByRole('button',{name:'Copy join information',exact:true}).waitFor();
 assert(!await page.locator('.page-host').innerText().then(text=>text.includes('synthetic-private-invitation')),'Invitation stays out of normal visible text');
 await page.locator('#fixture-language').selectOption('zh-CN');await page.getByRole('button',{name:'复制加入信息',exact:true}).waitFor();
 await page.locator('#fixture-join').click();await page.getByRole('button',{name:'取消邀请',exact:true}).click();
 await page.locator('.topology-inspector .machine-name').getByText('New Runner',{exact:true}).waitFor();
 assert(await page.locator('.topology-inspector').getByText('离线',{exact:true}).count()===1,'Registered but not online remains offline');
 assert(!await page.evaluate(()=>topologyFixture.calls.some(call=>call.body?.action==='remove_member')),'Cancel racing successful join never revokes actual membership');
 await page.setViewportSize({width:393,height:852});
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Narrow graph and inspector fit the viewport');
 await page.emulateMedia({reducedMotion:'reduce'});
 await page.waitForFunction(()=>{const signal=document.querySelector('.topology-signal');return signal&&getComputedStyle(signal).display==='none';});
 return {passed:true,agentOnly:true,identityProjection:true,localeDraft:true,expiryRepair:true,ownershipFence:true,joinedCancelRace:true,mobileWidth:393,reducedMotion:true};
}
