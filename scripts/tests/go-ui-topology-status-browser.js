// Production topology components with synthetic, payload-free node observations.
async page => {
 const assert=(value,message)=>{if(!value)throw Error(message);},errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 await page.setViewportSize({width:1280,height:1000});
 await page.goto('http://127.0.0.1:18743/scripts/tests/fixtures/topology.html');
 await page.locator('.topology-stats').filter({hasText:'2 tasks running'}).waitFor({timeout:10000});
 assert(!(await page.locator('.topology-stats').innerText()).includes('attention'),'No global attention count');
 const cloud=page.locator('.topology-node').filter({hasText:'Cloud Pot'}),codex=page.locator('.topology-node').filter({hasText:'Codex'});
 assert(await cloud.locator('.topology-issue-badge').count()===1,'Allowance warning is attached to its node');
 assert(await codex.locator('.topology-issue-badge').count()===0,'Ordinary running/queued work does not warn');
 assert((await codex.innerText()).includes('dev-box.local'),'A reported hostname identifies the remote node');
 await cloud.click();
 assert((await page.locator('.topology-issue-notice').innerText()).includes('token allowance'),'Selecting the problem node explains the cause');
 await codex.click();
 assert((await page.locator('.topology-assistant-state').allTextContents()).includes('Idle'),'Per-assistant idle state is visible');
 assert((await page.locator('.topology-assistant-state').allTextContents()).includes('Running'),'Per-assistant executing state is visible');
 assert((await page.locator('.topology-queues').innerText()).includes('3 pending'),'Local pending queue is not a delivery-page count');
 const chat=page.getByRole('button',{name:'Open chat · Code review',exact:true});await chat.focus();
 await page.evaluate(()=>topologyFixture.pages.refresh(['agent-links']));
 assert(await chat.evaluate(element=>element===document.activeElement),'Status refresh preserves assistant button focus');
 await page.evaluate(()=>{const link=topologyFixture.links.find(link=>link.id==='codex-link');link.status='offline';return topologyFixture.pages.refresh(['agent-links']);});
 assert((await page.locator('.topology-stats').innerText()).includes('1 tasks running'),'Offline node counts are excluded from current totals');
 assert((await page.locator('.topology-activity').innerText()).includes('Running 1 · Queued 2'),'Last known task counts remain in details');
 assert((await page.locator('.topology-node').filter({hasText:'Codex'}).innerText()).includes('Unknown'),'Offline activity is unknown rather than idle');
 await page.evaluate(()=>{topologyFixture.freezeObservations=true;const link=topologyFixture.links.find(link=>link.id==='codex-link');link.status='connected';link.observation.observed_at_ms=Date.now()-31000;return topologyFixture.pages.refresh(['agent-links']);});
 assert((await page.locator('.topology-node').filter({hasText:'Codex'}).innerText()).includes('Status unavailable or stale'),'A connected stale sample has its own node warning');
 await page.evaluate(()=>{topologyFixture.freezeObservations=false;return topologyFixture.pages.refresh(['agent-links']);});
 const edge=page.getByRole('button',{name:'Local Pot ↔ Codex · Agent connection',exact:true});await edge.click();
 assert(await page.locator('.topology-queues').count()===1,'Edge selection shows directional delivery stats');
 await page.evaluate(()=>topologyFixture.pages.refresh(['agent-links']));
 assert((await page.locator('.topology-inspector h2').innerText()).includes('Local Pot ↔ Codex'),'Edge selection survives status refresh');
 await page.locator('.topology-node.machine').click();await edge.click();await edge.focus();
 await page.evaluate(()=>topologyFixture.pages.refresh(['agent-links']));
 assert((await page.locator('.topology-inspector h2').innerText()).includes('Local Pot ↔ Codex'),'Edge selection also survives refresh after selecting a machine');
 assert(await edge.evaluate(element=>element===document.activeElement),'Keyboard focus on edges survives refresh');
 for(const locale of ['en','zh-CN']){
  await page.locator('#fixture-language').selectOption(locale);
  for(const theme of ['light','dark']){
   await page.locator('#fixture-theme').selectOption(theme);
   for(const width of [393,1280]){
    await page.setViewportSize({width,height:1000});
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Topology fits '+locale+' '+theme+' '+width);
    const bounds=await page.locator('.topology-node').evaluateAll(nodes=>nodes.map(node=>{const r=node.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height};}));
    for(let i=0;i<bounds.length;i++)for(let j=i+1;j<bounds.length;j++){const a=bounds[i],b=bounds[j];assert(!(a.x<b.x+b.width&&b.x<a.x+a.width&&a.y<b.y+b.height&&b.y<a.y+a.height),'Node labels do not overlap');}
   }
  }
 }
 assert(!errors.length,'No browser exceptions: '+errors.join('; '));
 return {passed:true,realComponents:true,nodeWarnings:true,staleCounts:true,queues:true,focus:true,locales:2,themes:2,widths:[393,1280]};
}
