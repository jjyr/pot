// Serve the repository root. All configuration requests use the disposable
// topology fixture; this test never exposes a real listener or changes Tailscale.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[];page.on('pageerror',error=>errors.push(error.message));
 const cdp=await page.context().newCDPSession(page);await cdp.send('Network.setCacheDisabled',{cacheDisabled:true});await cdp.send('Network.setBypassServiceWorker',{bypass:true});
 await page.goto(new URL('/scripts/tests/fixtures/topology.html',page.url()).href);
 await page.locator('#fixture-scenario').selectOption('access');
 await page.getByRole('button',{name:'Configure access',exact:true}).click();
 assert(await page.getByLabel('Access method',{exact:true}).inputValue()==='address','Existing access address is the default');
 assert((await page.getByLabel('Access address',{exact:true}).getAttribute('placeholder')).startsWith('tcp://100.'),'Private TCP is the default example');
 await page.getByText('Advanced local settings',{exact:true}).click();
 assert(await page.getByLabel('Local peer listener',{exact:true}).getAttribute('readonly')!==null,'The message listener stays read-only');
 await page.getByLabel('Access address',{exact:true}).fill('tcp://100.88.0.5:8793');await page.getByRole('button',{name:'Save',exact:true}).click();
 await page.locator('.agent-link-address').getByText('tcp://100.88.0.5:8793',{exact:true}).waitFor();
 assert(await page.evaluate(()=>topologyFixture.calls.some(call=>call.body?.action==='configure'&&call.body.mode==='address'&&call.body.endpoint==='tcp://100.88.0.5:8793')),'Private TCP is saved without a certificate');
 await page.locator('#fixture-scenario').selectOption('access');await page.getByRole('button',{name:'Configure access',exact:true}).click();
 await page.getByLabel('Access address',{exact:true}).fill('tcp://203.0.113.10:8793');await page.getByRole('button',{name:'Save',exact:true}).click();
 await page.getByText('Enter a reachable private TCP address, such as tcp://100.101.102.103:8793.',{exact:true}).waitFor();
 await page.locator('#fixture-language').selectOption('zh-CN');
 await page.getByText('请填写可达的私网 TCP 地址，例如 tcp://100.101.102.103:8793。',{exact:true}).waitFor();
 assert(await page.getByLabel('访问地址',{exact:true}).inputValue()==='tcp://203.0.113.10:8793','A rejected address and locale change preserve the draft');
 await page.getByLabel('访问地址',{exact:true}).fill('tcp://10.88.0.6:8793');await page.getByRole('button',{name:'保存',exact:true}).click();
 await page.locator('.agent-link-address').getByText('tcp://10.88.0.6:8793',{exact:true}).waitFor();
 await page.locator('#fixture-scenario').selectOption('tsmissing');await page.getByRole('button',{name:'配置访问',exact:true}).click();
 await page.getByLabel('访问方式',{exact:true}).selectOption('tailscale');
 assert(!await page.getByLabel('访问地址',{exact:true}).isVisible(),'Automatic Tailscale does not require a manual address');
 await page.getByRole('button',{name:'保存',exact:true}).click();await page.getByText('请先在此 Agent 所在机器安装 Tailscale，再重试。',{exact:true}).waitFor();
 await page.locator('#fixture-language').selectOption('en');await page.getByText('Install Tailscale on this Agent’s machine, then try again.',{exact:true}).waitFor();
 assert(await page.locator('pre').filter({hasText:'tailscale serve --bg --tcp=8793'}).count()===1,'Tailscale instructions create private TCP Serve without certificates');
 await page.locator('#fixture-scenario').selectOption('ready');await page.getByRole('button',{name:'Save',exact:true}).click();
 await page.locator('.agent-link-address').getByText('tcp://local-pot.fixture-tailnet.ts.net:8793',{exact:true}).waitFor();
 await page.setViewportSize({width:393,height:852});await page.locator('#fixture-scenario').selectOption('access');await page.getByRole('button',{name:'Configure access',exact:true}).click();
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'The access editor fits a narrow viewport');
 await page.setViewportSize({width:1280,height:900});await page.locator('#fixture-language').selectOption('zh-CN');await page.getByLabel('访问方式',{exact:true}).selectOption('address');
 await page.getByLabel('访问地址',{exact:true}).fill('');const guide=page.locator('details').filter({has:page.getByText('Tailscale 配置步骤',{exact:true})});if(await guide.getAttribute('open')===null)await page.getByText('Tailscale 配置步骤',{exact:true}).click();
 await page.screenshot({path:'output/pr-images/access-address-desktop-after.png'});
 assert(!errors.length,'No browser exceptions: '+errors.join('; '));
 return {passed:true,privateTCP:true,privateAddressRepair:true,tailscaleTCP:true,localeDraftAndErrors:true,readonlyListener:true,mobileWidth:393};
}
