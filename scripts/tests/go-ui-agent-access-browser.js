// Serve the repository root. All configuration requests use the disposable
// topology fixture; this test never exposes a real listener or changes Tailscale.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[];page.on('pageerror',error=>errors.push(error.message));
 const cdp=await page.context().newCDPSession(page);await cdp.send('Network.setCacheDisabled',{cacheDisabled:true});await cdp.send('Network.setBypassServiceWorker',{bypass:true});
 await page.goto(new URL('/scripts/tests/fixtures/topology.html',page.url()).href);
 await page.locator('#fixture-scenario').selectOption('access');
 await page.getByRole('button',{name:'Configure access',exact:true}).click();
 assert(await page.getByLabel('Access method',{exact:true}).inputValue()==='address','Existing access address is the default');
 assert((await page.getByLabel('Access address',{exact:true}).getAttribute('placeholder')).startsWith('http://100.'),'Private HTTP is the default example');
 await page.getByText('Advanced local settings',{exact:true}).click();
 assert(await page.getByLabel('Local message listener',{exact:true}).getAttribute('readonly')!==null,'The message listener stays read-only');
 await page.getByLabel('Access address',{exact:true}).fill('http://100.88.0.5:8793');await page.getByRole('button',{name:'Save',exact:true}).click();
 await page.locator('.agent-link-address').getByText('http://100.88.0.5:8793',{exact:true}).waitFor();
 assert(await page.evaluate(()=>topologyFixture.calls.some(call=>call.body?.action==='configure'&&call.body.mode==='address'&&call.body.endpoint==='http://100.88.0.5:8793')),'Private HTTP is saved without requiring HTTPS');
 await page.locator('#fixture-scenario').selectOption('access');await page.getByRole('button',{name:'Configure access',exact:true}).click();
 await page.getByLabel('Access address',{exact:true}).fill('http://203.0.113.10:8793');await page.getByRole('button',{name:'Save',exact:true}).click();
 await page.getByText('Enter a reachable Tailscale/VPN private HTTP address, or an existing HTTPS address.',{exact:true}).waitFor();
 await page.locator('#fixture-language').selectOption('zh-CN');
 await page.getByText('请填写可达的 Tailscale/VPN 私网 HTTP 地址，或已有的 HTTPS 地址。',{exact:true}).waitFor();
 assert(await page.getByLabel('访问地址',{exact:true}).inputValue()==='http://203.0.113.10:8793','A rejected address and locale change preserve the draft');
 await page.getByLabel('访问地址',{exact:true}).fill('https://agent.example.com');await page.getByRole('button',{name:'保存',exact:true}).click();
 await page.locator('.agent-link-address').getByText('https://agent.example.com',{exact:true}).waitFor();
 await page.locator('#fixture-scenario').selectOption('tsmissing');await page.getByRole('button',{name:'配置访问',exact:true}).click();
 await page.getByLabel('访问方式',{exact:true}).selectOption('tailscale');
 assert(!await page.getByLabel('访问地址',{exact:true}).isVisible(),'Automatic Tailscale does not require a manual address');
 await page.getByRole('button',{name:'保存',exact:true}).click();await page.getByText('请先在此 Agent 所在机器安装 Tailscale，再重试。',{exact:true}).waitFor();
 await page.locator('#fixture-language').selectOption('en');await page.getByText('Install Tailscale on this Agent’s machine, then try again.',{exact:true}).waitFor();
 assert(await page.locator('pre').filter({hasText:'tailscale serve --bg --http=8793'}).count()===1,'Tailscale instructions create private HTTP Serve without certificates');
 await page.locator('#fixture-scenario').selectOption('ready');await page.getByRole('button',{name:'Save',exact:true}).click();
 await page.locator('.agent-link-address').getByText('http://local-pot.fixture-tailnet.ts.net',{exact:true}).waitFor();
 await page.setViewportSize({width:393,height:852});await page.locator('#fixture-scenario').selectOption('access');await page.getByRole('button',{name:'Configure access',exact:true}).click();
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'The access editor fits a narrow viewport');
 await page.setViewportSize({width:1280,height:900});await page.locator('#fixture-language').selectOption('zh-CN');await page.getByLabel('访问方式',{exact:true}).selectOption('address');
 await page.getByLabel('访问地址',{exact:true}).fill('');const guide=page.locator('details').filter({has:page.getByText('Tailscale 配置步骤',{exact:true})});if(await guide.getAttribute('open')===null)await page.getByText('Tailscale 配置步骤',{exact:true}).click();
 await page.screenshot({path:'output/pr-images/access-address-desktop-after.png'});
 assert(!errors.length,'No browser exceptions: '+errors.join('; '));
 return {passed:true,privateHTTP:true,optionalHTTPS:true,privateAddressRepair:true,tailscaleHTTP:true,localeDraftAndErrors:true,readonlyListener:true,mobileWidth:393};
}
