// Run against a static repository server. Product components use only the fixture client.
import assert from 'node:assert/strict';
import {mkdir,readFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const [origin='http://127.0.0.1:17882',directory='output/pr-images/tasks-schedules']=process.argv.slice(2);
await mkdir(directory,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
const fixtureURL=(version,theme,locale='zh-CN')=>`${origin}/scripts/tests/fixtures/tasks-schedules.html?${version==='before'?'before=1&':''}locale=${locale}&theme=${theme}`;
async function openTasks(page){
 await page.waitForFunction(()=>window.tasksSchedulesFixture?.ready);
 const rail=page.locator('.rail-button[aria-label="任务"],.rail-button[aria-label="Tasks"]');
 if(await rail.isVisible())await rail.click();else{await page.locator('.narrow-navigation').click();await page.locator('.menu-destination[aria-label="任务"],.menu-destination[aria-label="Tasks"]').click();}
 await page.locator('.task-row').first().waitFor();await page.waitForFunction(()=>document.querySelector('.task-list')?.getAttribute('aria-busy')!=='true');
}
async function ready(page){await page.waitForFunction(()=>document.querySelector('.task-list')?.getAttribute('aria-busy')==='false');}
async function photosReady(page){await page.evaluate(()=>document.fonts.ready);await page.waitForFunction(()=>[...document.querySelectorAll('.record-group-heading img')].every(i=>i.complete&&i.naturalWidth===128));}
async function assertRecordLayout(page,selector){
 const rows=page.locator(selector);
 assert.ok(await rows.evaluateAll(nodes=>nodes.every(n=>n.getBoundingClientRect().height>=44&&n.scrollWidth<=n.clientWidth)),'Records retain 44px targets and fit their content');
 assert.ok(await page.locator('.record-group').evaluateAll(groups=>groups.every(g=>{const head=g.querySelector('.record-group-heading'),row=g.querySelector('button');return g.tagName==='SECTION'&&g.getAttribute('aria-labelledby')===head.querySelector('h2').id&&Math.abs(head.getBoundingClientRect().left-row.getBoundingClientRect().left)<1;})),'Semantic sections and rows share the same alignment');
 // Tab from the real identity disclosure into its first record, then through
 // the list to force scrolling. Focus must remain visible at both scroll edges.
 await page.locator('.record-owner-identity summary').first().focus();await page.keyboard.press('Tab');
 assert.equal(await rows.first().evaluate(n=>n===document.activeElement),true);
 for(const index of [0,await rows.count()-1]){
  if(index) {await rows.nth(index-1).focus();await page.keyboard.press('Tab');}
  assert.ok(await rows.nth(index).evaluate(n=>{const r=n.getBoundingClientRect(),list=n.closest('.page-host').getBoundingClientRect(),s=getComputedStyle(n);return n===document.activeElement&&n.matches(':focus-visible')&&parseFloat(s.outlineWidth)>=2&&r.top>=list.top-1&&r.bottom<=list.bottom+1&&r.left>=list.left&&r.right<=list.right;}),'Keyboard focus remains visible inside the page host');
 }
 await page.locator('.record-owner-identity summary').first().focus();await page.keyboard.press('Enter');assert.ok(await page.locator('.record-owner-identity').first().evaluate(n=>n.open),'Full identity remains keyboard accessible');await page.keyboard.press('Enter');
}
async function assertPageScroll(page,selector){
 const measurements=await page.locator('.record-group-list').evaluate(list=>{
  const host=list.closest('.page-host'),last=list.querySelector('.record-group:last-child button:last-child'),pager=host.querySelector('[data-view-key="pagination-bottom"]');
  list.scrollTop=1000;host.scrollTop=host.scrollHeight;
  const h=host.getBoundingClientRect(),r=last.getBoundingClientRect(),p=pager.getBoundingClientRect();
  return {height:list.clientHeight,content:list.scrollHeight,listTop:list.scrollTop,pageTop:host.scrollTop,pageRange:host.scrollHeight-host.clientHeight,lastVisible:r.bottom<=h.bottom&&r.top>=h.top,pagerVisible:p.bottom<=h.bottom&&p.top>=h.top,gap:p.top-list.getBoundingClientRect().bottom};
 });
 assert.ok(measurements.content<=measurements.height+1&&measurements.listTop===0,'Records have no independent scroll range '+JSON.stringify(measurements));
 assert.ok(measurements.lastVisible&&measurements.pagerVisible,'The page reaches its last record and bottom pager '+JSON.stringify(measurements));
 assert.ok(measurements.gap>=0&&measurements.gap<=12,'Bottom pager follows the natural list height');
 assert.ok(measurements.pageRange<=0||measurements.pageTop>0,'Overflow belongs to the page host');
 await assertRecordLayout(page,selector);
}
async function assertPhotos(page){
 await photosReady(page);
 const groups=await page.locator('.record-group').evaluateAll(nodes=>nodes.map(g=>({key:g.dataset.viewKey,src:g.querySelector('.record-group-heading img')?.getAttribute('src'),fallback:!!g.querySelector('.record-group-heading .avatar>svg')})));
 const avatars=await page.evaluate(()=>window.tasksSchedulesFixture.avatars);
 for(const g of groups){const [node,,id]=JSON.parse(g.key);if(id==='alpha')assert.equal(g.src,node==='node-a'?avatars.local:avatars.linked,'Same-name assistants retain exact profile avatar');else if(node==='node-a')assert.ok(g.fallback&&!g.src,'Missing avatar uses person icon');else assert.equal(g.src,avatars.research);}
}
const errors=[],captured=new Map(),palette=JSON.parse(await readFile(new URL('../../web/src/state/task-palette.json',import.meta.url),'utf8'));
try{
 for(const [name,width,height,theme]of [['desktop-light',1280,1000,'light'],['desktop-dark',1280,1000,'dark'],['mobile-light',390,1000,'light'],['mobile-dark',390,1000,'dark']])for(const version of ['before','after']){
  const page=await browser.newPage({viewport:{width,height},colorScheme:theme,reducedMotion:'reduce'});page.on('pageerror',e=>errors.push(e.message));
  await page.goto(fixtureURL(version,theme));await openTasks(page);const taskNames=(await page.locator('.task-row').evaluateAll(rows=>rows.map(row=>row.getAttribute('aria-label')).sort()));if(version==='before')captured.set(name,taskNames);else assert.deepEqual(taskNames,captured.get(name),'Before/after use the same global task page');await assertPhotos(page);await page.screenshot({path:resolve(directory,`tasks-${name}-${version}.png`)});
  if(name==='desktop-light'){await page.getByRole('button',{name:/^已完成 \d/}).click();await ready(page);await assertPhotos(page);await page.screenshot({path:resolve(directory,`tasks-completed-desktop-light-${version}.png`)});await page.getByRole('button',{name:/^进行中 \d/}).click();await ready(page);}
  await page.getByRole('button',{name:'定时任务',exact:true}).click();await page.locator('.schedule-record').first().waitFor();
  await assertPhotos(page);const scheduleNames=await page.locator('.schedule-record').evaluateAll(rows=>rows.map(r=>r.getAttribute('aria-label')).sort());if(version==='before')captured.set('schedules-'+name,scheduleNames);else assert.deepEqual(scheduleNames,captured.get('schedules-'+name),'Before/after use the same global schedule page');await page.screenshot({path:resolve(directory,`schedules-${name}-${version}.png`)});await page.close();
 }
 // Identical two-assistant / three-record catalogues exercise the reported clipping.
 for(const [name,width,theme]of [['small-desktop-light',1280,'light'],['small-phone-dark',390,'dark'],['small-narrow-light',320,'light']])for(const version of ['before','after']){
  const page=await browser.newPage({viewport:{width,height:800},colorScheme:theme,reducedMotion:'reduce'});page.on('pageerror',e=>errors.push(e.message));
  await page.goto(fixtureURL(version,theme)+'&small=1');await openTasks(page);await assertPhotos(page);
  assert.equal(await page.locator('.task-row').count(),3);assert.equal(await page.locator('.record-group').count(),2);
  await page.screenshot({path:resolve(directory,`tasks-${name}-${version}.png`)});
  await page.locator('.page-host').evaluate(host=>{host.scrollTop=host.scrollHeight;});
  await page.screenshot({path:resolve(directory,`tasks-${name}-page-end-${version}.png`)});
  if(version==='after')await assertPageScroll(page,'.task-row');
  await page.getByRole('button',{name:'定时任务',exact:true}).click();await page.locator('.schedule-record').first().waitFor();await assertPhotos(page);
  assert.equal(await page.locator('.schedule-record').count(),3);assert.equal(await page.locator('.record-group').count(),2);
  await page.screenshot({path:resolve(directory,`schedules-${name}-${version}.png`)});
  await page.locator('.page-host').evaluate(host=>{host.scrollTop=host.scrollHeight;});
  await page.screenshot({path:resolve(directory,`schedules-${name}-page-end-${version}.png`)});
  if(version==='after')await assertPageScroll(page,'.schedule-record');await page.close();
 }
 for(const [width,theme,locale,query]of [[1280,'light','en',''],[390,'dark','zh-CN','&small=1'],[320,'light','en','&long=1']]){
  const p=await browser.newPage({viewport:{width,height:800},colorScheme:theme,reducedMotion:'reduce'});
  await p.goto(fixtureURL('after',theme,locale)+'&verify-scroll=1'+query);
  await p.waitForFunction(()=>window.tasksSchedulesFixture?.scrollVerification,{timeout:30000});
  const result=await p.evaluate(()=>window.tasksSchedulesFixture.scrollVerification);assert.ok(result.passed,result.error);await p.close();
 }
 const system=await browser.newPage({viewport:{width:390,height:800},colorScheme:'dark',reducedMotion:'reduce'});
 await system.goto(fixtureURL('after','system','en')+'&small=1');await openTasks(system);
 const darkCanvas=await system.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--canvas'));
 await system.emulateMedia({colorScheme:'light'});
 assert.notEqual(await system.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--canvas')),darkCanvas,'System appearance follows the browser preference');
 await assertPageScroll(system,'.task-row');await system.getByRole('button',{name:'Schedules',exact:true}).click();await system.locator('.schedule-record').first().waitFor();
 await system.emulateMedia({colorScheme:'dark'});assert.equal(await system.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--canvas')),darkCanvas);
 await assertPageScroll(system,'.schedule-record');await system.close();
 const page=await browser.newPage({viewport:{width:1280,height:1000},reducedMotion:'reduce'});page.on('pageerror',e=>errors.push(e.message));
 await page.goto(fixtureURL('after','light'));await openTasks(page);
 assert.equal(await page.locator('.task-row').count(),20);
 assert.equal(await page.locator('.record-group').count(),4,'Full identity groups merge nonadjacent records and keep same-name nodes separate');
 const counts=await page.locator('.record-group-count').allTextContents();assert.equal(counts.reduce((n,s)=>n+Number(s.match(/\d+/)[0]),0),20);
 assert.equal(await page.locator('.record-pagination').count(),2);
 // Verify real catalogue addresses rather than local linked route IDs, including
 // duplicate prefixes and equal IDs across nodes without affecting routing.
 assert.ok(await page.locator('.task-short-id').evaluateAll(nodes=>nodes.every(n=>/^#[a-f0-9]{4}$/.test(n.textContent))));
 const headingAvatars=await page.locator('.record-group').evaluateAll(groups=>groups.map(g=>({key:g.dataset.viewKey,src:g.querySelector('.record-group-heading img')?.getAttribute('src'),loaded:g.querySelector('.record-group-heading img')?.naturalWidth||0,fallback:!!g.querySelector('.record-group-heading .avatar>svg')})));
 const fixtureAvatars=await page.evaluate(()=>window.tasksSchedulesFixture.avatars);
 assert.equal(headingAvatars.find(g=>g.key==='["node-a","assistant","alpha"]').src,fixtureAvatars.local);
 assert.equal(headingAvatars.find(g=>g.key==='["node-b","assistant","alpha"]').src,fixtureAvatars.linked);
 assert.ok(headingAvatars.filter(g=>g.src).every(g=>g.loaded===128));
 assert.ok(headingAvatars.find(g=>g.key==='["node-a","assistant","beta"]').fallback,'Missing profile uses person icon');
 // An avatar-only directory change must refresh the mounted heading. Broken PNG
 // decoding uses the same fallback, including after DOM reconciliation.
 await page.evaluate(()=>{window.tasksSchedulesFixture.profiles.two[1].avatar='data:image/png;base64,broken';window.tasksSchedulesFixture.change(['agent-links']);});
 const linkedResearch=page.locator('.record-group').filter({has:page.locator('h2',{hasText:'Research'})}).filter({has:page.locator('.record-group-source',{hasText:'Research node'})});
 await linkedResearch.locator('.avatar>svg').waitFor();assert.equal(await linkedResearch.locator('.avatar img').count(),0);
 await page.evaluate(()=>{window.tasksSchedulesFixture.profiles.two[1].avatar=window.tasksSchedulesFixture.avatars.research;window.tasksSchedulesFixture.change(['agent-links']);});
 await linkedResearch.locator('.avatar img').waitFor();await page.waitForFunction(()=>[...document.querySelectorAll('.record-group-heading img')].every(i=>i.complete&&i.naturalWidth===128));

 await page.evaluate(()=>{window.retainedTask=document.querySelector('.task-row');window.retainedGroup=document.querySelector('.record-group');document.querySelector('.record-owner-identity').open=true;window.tasksSchedulesFixture.change();});
 await ready(page);const retained=await page.evaluate(()=>({task:window.retainedTask===document.querySelector('.task-row'),group:window.retainedGroup===document.querySelector('.record-group'),open:document.querySelector('.record-owner-identity').open}));assert.ok(retained.task&&retained.group&&retained.open,'Background refresh preserves DOM and disclosures '+JSON.stringify(retained));
 await page.getByRole('button',{name:/^已完成 \d/}).click();await ready(page);
 assert.equal(await page.locator('.task-row').count(),10);assert.ok((await page.locator('.task-row').evaluateAll(rows=>rows.every(r=>r.dataset.phase==='complete'))));

 await page.getByRole('button',{name:/^失败 \d/}).click();await ready(page);assert.equal(await page.locator('.task-row').count(),10);assert.ok(await page.locator('.task-row').evaluateAll(rows=>rows.every(r=>r.dataset.phase==='failed')));
 await page.getByRole('combobox',{name:'按助手筛选'}).selectOption(JSON.stringify(['node-b','assistant','alpha']));await ready(page);
 assert.equal(await page.locator('.record-group').count(),1);assert.equal(await page.locator('.task-row').count(),2);
 const query=await page.evaluate(()=>window.tasksSchedulesFixture.calls.filter(c=>c.path.startsWith('/api/v1/tasks/catalog')).at(-1));assert.ok(query.path.includes('assistant_id=agent-link%3Aresearch%3Aalpha')&&query.path.includes('phase=failed'));assert.equal(query.options.space,'two');
 await page.getByRole('combobox',{name:'按助手筛选'}).selectOption('');await ready(page);await page.getByRole('button',{name:/^进行中 \d/}).click();await ready(page);
 await page.evaluate(()=>window.tasksSchedulesFixture.delay=250);await page.locator('[data-view-key="pagination-top"] [data-page-direction="next"]').click();
 await page.waitForFunction(()=>document.querySelector('.task-list').getAttribute('aria-busy')==='true');assert.equal(await page.locator('.record-pagination button:disabled').count(),4);await ready(page);assert.match(await page.locator('.record-page-summary').first().textContent(),/第 2 页/);assert.ok(await page.locator('.record-continuation').count()>0);
 await page.locator('[data-view-key="pagination-top"] [data-page-direction="next"]').click();await ready(page);assert.equal(await page.locator('.task-row').count(),1);assert.equal(await page.locator('[data-view-key="pagination-top"] [data-page-direction="previous"]').evaluate(el=>el===document.activeElement),true,'Last-page navigation restores focus to enabled Previous');
 await page.getByRole('searchbox',{name:'搜索任务标题或 ID…'}).fill('no matching fixture');await page.waitForTimeout(500);await ready(page);assert.equal(await page.locator('.task-row').count(),0);assert.equal(await page.locator('.record-pagination button:disabled').count(),4);assert.match(await page.locator('.record-page-summary').first().textContent(),/共 0 项/);
 await page.getByRole('searchbox',{name:'搜索任务标题或 ID…'}).fill('');await page.waitForTimeout(500);await ready(page);
 await page.getByRole('button',{name:'定时任务',exact:true}).click();await page.locator('.schedule-record').first().waitFor();
 assert.equal(await page.locator('.schedule-record').count(),20);assert.equal(await page.locator('.record-pagination').count(),2);
 await assertPhotos(page);
 await page.evaluate(()=>{window.tasksSchedulesFixture.profiles.two[1].avatar='data:image/png;base64,broken';window.tasksSchedulesFixture.change(['agent-links']);});
 await page.locator('.record-group').filter({has:page.locator('.record-group-source',{hasText:'Research node'})}).filter({has:page.locator('h2',{hasText:'Research'})}).locator('.avatar>svg').waitFor();
 await page.evaluate(()=>{window.tasksSchedulesFixture.profiles.two[1].avatar=window.tasksSchedulesFixture.avatars.research;window.tasksSchedulesFixture.change(['agent-links']);});
 await photosReady(page);

 await page.locator('[data-view-key="pagination-top"] [data-page-direction="next"]').click();assert.match(await page.locator('.record-page-summary').first().textContent(),/第 2 页/);assert.equal(await page.locator('.schedule-record').count(),20);
 await page.getByRole('combobox',{name:'按助手筛选'}).selectOption('unknown-owner');assert.equal(await page.locator('.schedule-record').count(),1);assert.equal(await page.locator('.record-group h2').innerText(),'归属信息缺失');
 const calls=await page.evaluate(()=>window.tasksSchedulesFixture.calls.length);await page.locator('.schedule-record').click();assert.equal(await page.evaluate(()=>window.tasksSchedulesFixture.calls.length),calls,'Unknown ownership never falls back to selected assistant');
 await page.close();
 // Existing product actions retain exact owner scope and use real component handlers.
 const actions=await browser.newPage({viewport:{width:1280,height:1000}});actions.on('pageerror',e=>errors.push(e.message));
 await actions.goto(fixtureURL('after','light','en'));await openTasks(actions);
 await actions.locator('.task-row').first().click();await actions.locator('.task-goal').waitFor();
 assert.ok(await actions.locator('.task-goal .task-identity-mark[data-task-color]').count());
 const confirmed=await actions.evaluate(async()=>{const {taskIdentities}=await import('/web/src/state/task-identity.mjs');return taskIdentities.resolve(window.tasksSchedulesFixture.tasks[0].address);});assert.equal(confirmed._space_id,'two');assert.equal(confirmed.assistant_id,'agent-link:research:beta','Detail refresh retains the confirmed catalogue owner');
 await actions.getByRole('button',{name:'Task actions',exact:true}).click();await actions.getByRole('button',{name:'Pause task',exact:true}).click();
 await actions.waitForFunction(()=>window.tasksSchedulesFixture.calls.some(c=>c.options.body?.action==='pause'&&c.path.endsWith('/control')));
 let call=await actions.evaluate(()=>window.tasksSchedulesFixture.calls.find(c=>c.path.endsWith('/control')));assert.equal(call.options.owner,'agent-link:research:beta');assert.equal(call.options.space,'two');assert.equal(call.options.body.run_id,'fixture-run');
 await actions.getByRole('button',{name:'Task actions',exact:true}).click();await actions.getByRole('button',{name:'Resume task',exact:true}).click();
 await actions.locator('.task-actions[open]').waitFor({state:'hidden'});await actions.getByRole('button',{name:'Task actions',exact:true}).click();await actions.getByRole('button',{name:'Cancel task',exact:true}).click();await actions.getByRole('button',{name:'Cancel task',exact:true}).click();
 await actions.waitForFunction(()=>window.tasksSchedulesFixture.calls.some(c=>c.path.endsWith('/cancel')&&c.options.method==='POST'));
 await actions.goto(fixtureURL('after','light','en'));await openTasks(actions);await actions.getByRole('button',{name:/^Completed \d/}).click();await ready(actions);await actions.locator('.task-row').first().click();await actions.locator('.task-goal').waitFor();
 await actions.getByRole('button',{name:'Task actions',exact:true}).click();assert.equal(await actions.getByRole('button',{name:'Cancel task',exact:true}).count(),0);await actions.getByRole('button',{name:'Archive task',exact:true}).click();
 await actions.waitForFunction(()=>window.tasksSchedulesFixture.calls.some(c=>c.path.endsWith('/dispositions')&&c.options.body?.decisions?.[0]?.archived));
 await actions.locator('.task-composer-area textarea').fill('Synthetic follow-up');await actions.getByRole('button',{name:'Send follow-up',exact:true}).click();
 await actions.waitForFunction(()=>window.tasksSchedulesFixture.calls.some(c=>c.path.endsWith('/messages')&&c.options.method==='POST'));
 await actions.goto(fixtureURL('after','light','en'));await openTasks(actions);await actions.getByRole('button',{name:'Schedules',exact:true}).click();await actions.locator('.schedule-record').first().click();await actions.getByRole('button',{name:'Run now',exact:true}).waitFor();
 const tasksBefore=await actions.evaluate(()=>window.tasksSchedulesFixture.tasks.length);await actions.getByRole('button',{name:'Run now',exact:true}).click();await actions.waitForFunction(()=>window.tasksSchedulesFixture.calls.some(c=>c.options.body?.action==='run'));
 assert.equal(await actions.evaluate(()=>window.tasksSchedulesFixture.tasks.length),tasksBefore,'Synthetic action cannot create real/model work');
 await actions.getByRole('button',{name:'Resume',exact:true}).click();await actions.getByRole('button',{name:'Pause',exact:true}).waitFor();await actions.getByRole('button',{name:'Pause',exact:true}).click();
 await actions.getByRole('button',{name:'View task',exact:true}).click();await actions.locator('.task-goal').waitFor();assert.ok(await actions.locator('.task-goal .task-short-id').textContent());await actions.close();
 // Async failures and locale changes never mislabel prior-view records or fabricate totals.
 const asyncPage=await browser.newPage({viewport:{width:390,height:1000}});asyncPage.on('pageerror',e=>errors.push(e.message));await asyncPage.goto(fixtureURL('after','light'));await openTasks(asyncPage);
 await asyncPage.evaluate(()=>{window.tasksSchedulesFixture.offline=true;window.tasksSchedulesFixture.delay=250;});await asyncPage.getByRole('button',{name:/^已完成 \d/}).click();await ready(asyncPage);assert.equal(await asyncPage.locator('.task-row').count(),0);
 await asyncPage.evaluate(()=>{window.tasksSchedulesFixture.offline=false;window.tasksSchedulesFixture.preference('locale','en');});await asyncPage.locator('.task-row').first().waitFor();await ready(asyncPage);assert.ok(await asyncPage.locator('.task-row').evaluateAll(rows=>rows.every(r=>r.dataset.phase==='complete')));assert.match(await asyncPage.locator('.record-page-summary').first().textContent(),/Page 1/);
 await asyncPage.evaluate(()=>{window.tasksSchedulesFixture.offline=true;window.tasksSchedulesFixture.change();});await asyncPage.waitForTimeout(600);await ready(asyncPage);assert.match(await asyncPage.locator('.record-page-summary').first().textContent(),/Sources incomplete/);assert.ok(await asyncPage.locator('.task-row:disabled').count());assert.ok(!(await asyncPage.locator('.record-page-summary').first().textContent()).includes('records'));
 await asyncPage.close();
 // Independent collision examples preserve the exact four characters, including
 // equal real IDs across nodes; detail reads still use distinct local route IDs.
 for(const version of ['before','after']){
  const identity=await browser.newPage({viewport:{width:1280,height:1000},reducedMotion:'reduce'});identity.on('pageerror',e=>errors.push(e.message));await identity.goto(fixtureURL(version,'light'));await openTasks(identity);
  await identity.evaluate(()=>{const f=window.tasksSchedulesFixture;f.tasks[0].address.agent_id=f.tasks[1].address.agent_id;f.tasks[3].address.agent_id=f.tasks[1].address.agent_id.slice(0,4)+'ffff-aaaa-4bbb-8ccc-000000000003';f.change();});await ready(identity);
  await photosReady(identity);await identity.screenshot({path:resolve(directory,`tasks-prefix-collision-desktop-light-${version}.png`)});
  if(version==='after'){
   const labels=await identity.locator('.task-row').evaluateAll(rows=>Object.fromEntries(rows.map(r=>[r.getAttribute('aria-label'),r.querySelector('.task-short-id').textContent])));
   for(const i of [0,1,3])assert.equal(labels[await identity.evaluate(i=>window.tasksSchedulesFixture.tasks[i].title,i)],'#a827');
   await identity.locator('[data-view-key="pagination-top"] [data-page-direction="next"]').click();await ready(identity);await identity.locator('[data-view-key="pagination-top"] [data-page-direction="previous"]').click();await ready(identity);
   assert.deepEqual(await identity.locator('.task-row').evaluateAll(rows=>Object.fromEntries(rows.map(r=>[r.getAttribute('aria-label'),r.querySelector('.task-short-id').textContent]))),labels,'Pagination cannot change short labels');
   for(const i of [0,1,3]){
    const task=await identity.evaluate(i=>window.tasksSchedulesFixture.tasks[i],i);await identity.getByRole('button',{name:task.title,exact:true}).click();await identity.locator('.task-goal').waitFor();assert.equal(await identity.locator('.task-goal .task-short-id').textContent(),'#a827');
    const read=await identity.evaluate(id=>window.tasksSchedulesFixture.calls.filter(c=>c.path==='/api/v1/tasks/'+encodeURIComponent(id)).at(-1),task.id);assert.equal(read.options.space,task.space);assert.equal(read.options.owner,task.assistant_id);
    await openTasks(identity);
   }
  }
  await identity.close();
  // Failure is present before mount in both sources, so this pair demonstrates
  // the actual broken-image fallback rather than different fixture content.
  const broken=await browser.newPage({viewport:{width:1280,height:1000},reducedMotion:'reduce'});broken.on('pageerror',e=>errors.push(e.message));await broken.goto(fixtureURL(version,'light'));await broken.waitForFunction(()=>window.tasksSchedulesFixture?.ready);
  await broken.evaluate(()=>{window.tasksSchedulesFixture.profiles.two[1].avatar='data:image/png;base64,broken';window.tasksSchedulesFixture.change(['agent-links']);});
  await broken.waitForFunction(()=>window.tasksSchedulesFixture.client.assistants.some(a=>a.origin?.assistant_id==='beta'&&a.avatar==='data:image/png;base64,broken'));await openTasks(broken);
  await broken.locator('.record-group').first().locator('.avatar>svg').waitFor();
  await broken.screenshot({path:resolve(directory,`tasks-avatar-failure-desktop-light-${version}.png`)});await broken.close();
 }
 // Narrow layouts, both catalog languages, light/dark/system and keyboard touch geometry.
 for(const width of [320,390,1280])for(const theme of ['light','dark','system'])for(const locale of ['en','zh-CN']){
  const p=await browser.newPage({viewport:{width,height:1000},colorScheme:theme==='system'?'dark':theme,reducedMotion:'reduce'});p.on('pageerror',e=>errors.push(e.message));await p.goto(fixtureURL('after',theme,locale));await openTasks(p);
  assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`${width}/${theme}/${locale} horizontal overflow`);
  const geometry=await p.locator('.record-pagination button').evaluateAll(nodes=>nodes.every(n=>n.getBoundingClientRect().height>=44));assert.ok(geometry);await assertPhotos(p);assert.ok(await p.locator('.task-short-id').evaluateAll(nodes=>nodes.every(n=>/^#[a-f0-9]{4}$/.test(n.textContent))));
  const color=await p.locator('.task-identity-mark[data-task-color]').first().evaluate(el=>({index:Number(el.dataset.taskColor),bg:getComputedStyle(el).getPropertyValue('--task-bg').trim()}));assert.equal(color.bg,palette[color.index][theme==='light'?'light':'dark'].bg);
  await assertPageScroll(p,'.task-row');
  await p.getByRole('button',{name:locale==='en'?'Schedules':'定时任务',exact:true}).click();await p.locator('.schedule-record').first().waitFor();await assertPhotos(p);assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);assert.ok(await p.locator('.record-pagination button').evaluateAll(nodes=>nodes.every(n=>n.getBoundingClientRect().height>=44)));await assertPageScroll(p,'.schedule-record');await p.close();
 }
 assert.deepEqual(errors,[]);console.log('PASS: actual Tasks/Schedules pages, full-query adapter, global pagination, complete/phase/owner filters, retained DOM/disclosures, loading/empty/focus, unknown ownership, fixed four-character IDs with collisions/cross-node detail routing, loaded profile avatars/missing/decode failure/avatar-only refresh on both pages 18 layout/locale/theme combinations, natural page scrolling for small/long catalogues, retained page position, visible pager focus and live system appearance.');
}finally{await browser.close();}
