import assert from 'node:assert/strict';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {mobileChatBackHTTPFixture} from './mobile-chat-back-http-fixture.mjs';

const baseline=process.argv.includes('--baseline'),label=baseline?'before':'after';
if(baseline&&!process.env.POT_BACK_UI_SOURCE)throw Error('Baseline requires the exact original ui.js module via POT_BACK_UI_SOURCE');
const output=resolve(process.env.POT_BACK_OUTPUT||'.pot-artifacts/mobile-chat-back');await mkdir(output,{recursive:true});
const source=await sourceServer(),fixture=await mobileChatBackHTTPFixture(source.origin,{uiSource:process.env.POT_BACK_UI_SOURCE?await readFile(process.env.POT_BACK_UI_SOURCE,'utf8'):undefined});
const browser=await launchBrowser(),results=[];
let context,page;const errors=[],consoleErrors=[];
const setup=async()=>{
  if(context)await context.close();
  context=await browser.newContext({viewport:{width:390,height:852},hasTouch:true,isMobile:true,serviceWorkers:'block'});
  await context.route('**/*',route=>new URL(route.request().url()).origin===fixture.origin?route.continue():route.abort());
  page=await context.newPage();page.setDefaultTimeout(8000);page.on('pageerror',error=>errors.push(error.message));page.on('console',message=>{if(message.type()==='error')consoleErrors.push(message.text());});
await context.addInitScript(()=>{
  localStorage.setItem('pot-system-notifications-browser-v1','false');
  const evidence=window.backEvidence={events:[],history:[],states:[],draftWaits:[],frameEvents:[]};let previous;
  const sample=reason=>{const root=document.querySelector('#app'),h=document.querySelector('.history[role="log"]:not([data-preparation-surface])'),selected=document.querySelector('.assistant-row.is-selected');const value={route:root?.dataset.route,directory:root?.classList.contains('show-directory'),selected:selected?.dataset.viewKey,title:document.querySelector('.window-title')?.textContent,chatVisible:!!h&&h.getBoundingClientRect().height>0&&getComputedStyle(h).visibility==='visible',directoryVisible:!!root?.querySelector('.assistant-directory')?.getBoundingClientRect().height,messages:[...(h?.querySelectorAll('article.message')||[])].slice(0,3).map(row=>row.dataset.messageId),url:location.href};const key=JSON.stringify(value);if(key!==previous){evidence.states.push({at:Date.now(),reason,...value});previous=key;}};
  new MutationObserver(()=>sample('mutation')).observe(document,{subtree:true,childList:true,attributes:true,attributeFilter:['class','data-route','style']});
  for(const method of ['pushState','replaceState']){const original=history[method];history[method]=function(state,...args){evidence.history.push({at:Date.now(),method,state});return original.call(this,state,...args);};}
  addEventListener('popstate',event=>evidence.history.push({at:Date.now(),method:'popstate',trusted:event.isTrusted,state:event.state,url:location.href}));
  for(const type of ['pointerdown','pointermove','pointerup','pointercancel','touchstart','touchmove','touchend'])document.addEventListener(type,event=>evidence.events.push({at:Date.now(),type,trusted:event.isTrusted,pointerType:event.pointerType,x:event.clientX,y:event.clientY,inCode:!!event.target.closest?.('pre')}),true);
});
};
const key=owner=>JSON.stringify(['local',owner]);
const selected=()=>page.locator('.assistant-row.is-selected').getAttribute('data-view-key');
const directory=()=>page.locator('#app').evaluate(root=>root.classList.contains('show-directory'));
const ready=()=>page.waitForFunction(()=>{const h=document.querySelector('.history[role="log"]:not([data-preparation-surface])');return h&&h.getBoundingClientRect().height>0&&getComputedStyle(h).visibility==='visible'&&!h.hasAttribute('aria-busy')&&!h.querySelector('.message-window[aria-busy]')&&h.querySelector('article.message')&&!document.querySelector('#pot-composer')?.disabled;});
const open=async owner=>{await page.locator('.assistant-row').filter({hasText:owner==='main'?'Pot':owner==='beta'?'Beta':'Gamma'}).tap();await page.waitForFunction(owner=>document.querySelector('.assistant-row.is-selected')?.dataset.viewKey===JSON.stringify(['local',owner])&&history.state?.potScope?.owner===owner&&history.state.potDirectory===false,owner);await ready();assert.equal(await selected(),key(owner));assert.equal(await directory(),false);};
const sample=()=>page.evaluate(()=>{const h=document.querySelector('.history[role="log"]:not([data-preparation-surface])'),rect=h.getBoundingClientRect();const row=[...h.querySelectorAll('article.message')].find(row=>{const box=row.getBoundingClientRect();return Math.min(box.bottom,rect.bottom)-Math.max(box.top,rect.top)>=24;});return {directoryVisible:!!document.querySelector('.assistant-directory')?.getBoundingClientRect().height,chatVisible:rect.height>0&&getComputedStyle(h).visibility==='visible',history:history.state,selected:document.querySelector('.assistant-row.is-selected')?.dataset.viewKey,directory:document.querySelector('#app').classList.contains('show-directory'),input:document.querySelector('#pot-composer')?.value,anchor:row?.dataset.messageId,offset:row?row.getBoundingClientRect().top-rect.top:null,scroll:h.scrollTop,url:location.href};});
// Poll completed readonly transactions, including all storage identities. An
// impossible text condition below must reach the deadline rather than pass.
const committed=(owner,text,timeout=8000)=>page.evaluate(async expected=>{
  const {createIndexedStore,idbRequest}=await import('/state/indexed-store.mjs'),store=createIndexedStore(),started=performance.now();let checks=0,last;
  const ownerID=JSON.stringify(['local',expected.owner]),draftKey=JSON.stringify(['back-control','local',expected.owner]),stateKey=JSON.stringify(['back-control',ownerID,expected.owner+'-conversation']);
  try{while(performance.now()-started<expected.timeout){
    const [draft,startup,chats]=await store.transaction(['composerDrafts','startup','conversations'],'readonly',tx=>Promise.all([idbRequest(tx.objectStore('composerDrafts').get(draftKey)),idbRequest(tx.objectStore('startup').get('last')),idbRequest(tx.objectStore('conversations').getAll())]));checks++;
    const snapshot=chats.find(row=>row.projection.runtime_id==='back-control'&&row.projection.owner_id===ownerID&&row.projection.conversation_id===expected.owner+'-conversation')?.projection;
    const input=document.querySelector('#pot-composer');last={draft,startup,snapshotBoot:snapshot?.boot_id,selected:document.querySelector('.assistant-row.is-selected')?.dataset.viewKey};
    if(startup?.runtime_id==='back-control'&&startup.boot_id==='back-boot'&&startup.space==='local'&&startup.owner===expected.owner&&startup.surface==='browser:'+sessionStorage.getItem('pot-chat-view-id')&&snapshot?.boot_id==='back-boot'&&draft?.key===draftKey&&draft.state_key===stateKey&&draft.conversation_id===expected.owner+'-conversation'&&draft.text===expected.text&&!draft.pending&&last.selected===ownerID&&input?.isConnected&&input===document.querySelector('#pot-composer')&&!input.disabled&&input.value===expected.text){const proof={kind:'matched',completedAt:Date.now(),checks,elapsed:performance.now()-started,expected,last};backEvidence.draftWaits.push(proof);return proof;}
    await new Promise(requestAnimationFrame);
  }backEvidence.draftWaits.push({kind:'deadline',checks,elapsed:performance.now()-started,expected,last});throw Error('Committed draft deadline: '+JSON.stringify({checks,elapsed:performance.now()-started,last}));}finally{store.close();}
},{owner,text,timeout});
const capture=async name=>{
  const value={name,at:Date.now(),state:await sample(),evidence:await page.evaluate(()=>window.backEvidence)};
  await writeFile(output+'/'+name+'-'+label+'.json',JSON.stringify(value,null,2));return value;
};
const startDirectory=async()=>{await setup();await page.goto(fixture.origin+'/?locale=en&theme=light');await page.locator('.show-directory .assistant-row').first().waitFor();await page.waitForFunction(()=>history.state?.potDirectory===true&&history.state?.potScope?.owner==='main');};
try{
  for(const owner of ['main','beta','gamma']){
    await startDirectory();await open(owner);await page.locator('#pot-composer').fill('Ordinary unsent '+owner);await page.locator('#pot-composer').blur();
    const saved=await committed(owner,'Ordinary unsent '+owner);if(owner==='main'){await assert.rejects(committed(owner,'Impossible text',250),/Committed draft deadline/);const negative=await page.evaluate(()=>backEvidence.draftWaits.at(-1));assert.equal(negative.kind,'deadline');assert(negative.elapsed>=250&&negative.checks>1);results.push({kind:'impossible-draft-rejected',proof:negative});}
    const cdp=await context.newCDPSession(page),start=await sample(),inputMark=await page.evaluate(()=>({events:backEvidence.events.length,history:backEvidence.history.length}));
    // Browser touch input: a small first horizontal move lets the app claim its
    // existing edge intent before Chromium's native panning threshold.
    for(const [type,x]of [['touchStart',5],['touchMove',19],['touchMove',60],['touchMove',160],['touchEnd',160]]){await cdp.send('Input.dispatchTouchEvent',{type,touchPoints:type==='touchEnd'?[]:[{x,y:260}]});await page.evaluate(()=>new Promise(requestAnimationFrame));}
    await page.waitForFunction(()=>!document.querySelector('#app').classList.contains('is-edge-back'));
    const end=await sample();results.push({kind:'native-touch-edge',owner,start,end,saved});assert(end.directory&&end.directoryVisible&&!end.chatVisible,'Trusted edge swipe shows the actual directory');assert.equal(end.selected,key(owner));const input=await page.evaluate(mark=>({events:backEvidence.events.slice(mark.events),history:backEvidence.history.slice(mark.history)}),inputMark);assert(input.events.some(event=>event.type==='pointerup'&&event.pointerType==='touch'&&event.trusted),'Browser supplied a trusted completed touch');assert(input.history.some(event=>event.method==='replaceState'&&event.state.potDirectory&&event.state.potScope.owner===owner),'App edge Back records the directory');assert(!input.history.some(event=>event.method==='popstate'),'App edge case is separate from browser history Back');results.push({kind:'touch-path',owner,...input});
    await capture('touch-'+owner);await open(owner);assert.equal(await page.locator('#pot-composer').inputValue(),'Ordinary unsent '+owner);
    // A content gesture away from the edge must stay in the code scroller.
    const code=page.locator('.history[role="log"]:not([data-preparation-surface]) pre');
    await code.scrollIntoViewIfNeeded();const box=await code.boundingBox();assert(await page.evaluate(y=>!!document.elementFromPoint(250,y)?.closest('pre'),box.y+Math.min(20,box.height/2)),'Horizontal input begins inside the actual code block');
    for(const [type,x]of [['touchStart',250],['touchMove',300],['touchEnd',300]])await cdp.send('Input.dispatchTouchEvent',{type,touchPoints:type==='touchEnd'?[]:[{x,y:box.y+Math.min(20,box.height/2)}]});
    assert.equal(await directory(),false,'Horizontal code content stays in Chat');
    // A frame owns its own pointer document. This synthetic frame is an input
    // boundary probe, not a product resource/readiness replacement.
    await page.evaluate(()=>{const frame=document.createElement('iframe');frame.id='boundary-probe';frame.srcdoc='<body style="width:1200px">Synthetic horizontal frame</body>';frame.style.cssText='position:fixed;left:40px;top:300px;width:300px;height:90px;z-index:100';document.body.append(frame);});
    await page.waitForFunction(()=>document.querySelector('#boundary-probe')?.contentDocument?.readyState==='complete'&&document.querySelector('#boundary-probe').contentDocument.body?.textContent.includes('Synthetic horizontal frame'));await page.evaluate(()=>{const doc=document.querySelector('#boundary-probe').contentDocument;for(const type of ['pointerdown','pointercancel','pointerup','touchend'])doc.addEventListener(type,event=>backEvidence.frameEvents.push({type,trusted:event.isTrusted,pointerType:event.pointerType}),true);});
    for(const [type,x]of [['touchStart',60],['touchMove',150],['touchEnd',150]])await cdp.send('Input.dispatchTouchEvent',{type,touchPoints:type==='touchEnd'?[]:[{x,y:330}]});
    assert.equal(await directory(),false,'Frame gesture stays in Chat');const frameEvents=await page.evaluate(()=>backEvidence.frameEvents);assert(frameEvents.some(event=>event.type==='pointerdown'&&event.trusted&&event.pointerType==='touch'),'The frame document receives real touch input');results.push({kind:'horizontal-boundaries',owner,codeInput:{fromX:250,toX:300,y:box.y+Math.min(20,box.height/2)},frameInput:{fromX:60,toX:150,y:330},frameEvents,state:await sample()});await page.locator('#boundary-probe').evaluate(node=>node.remove());await capture('boundaries-'+owner);
    await page.locator('.titlebar .narrow-navigation').tap();await page.screenshot({path:output+'/directory-'+owner+'-'+label+'.png'});
    // Reopening the exact directory history entry must not close it just because
    // the cached startup identity points at the retained conversation.
    await page.reload();await page.locator('.assistant-row.is-selected').waitFor({state:'attached'});
    await page.waitForFunction(()=>history.state?.potScope?.owner&&document.querySelector('.assistant-row.is-selected'));
    const reload=await sample();results.push({kind:'reload-directory',owner,state:reload});
    if(!baseline){assert(reload.directory&&reload.directoryVisible&&!reload.chatVisible,'Reload keeps the actual directory visible');assert.equal(reload.selected,key(owner));}
    await capture('reload-'+owner);
  }
  for(const owner of ['beta','gamma']){
    await startDirectory();const parent=await sample();await open(owner);
    await page.locator('#pot-composer').fill('Reading draft '+owner);await page.locator('#pot-composer').blur();await committed(owner,'Reading draft '+owner);
    // Supplemental deterministic reading input; real controller remains sole
    // writer of restoration and persists the resulting reading position.
    await page.locator('.history[role="log"]:not([data-preparation-surface])').evaluate(async h=>{h.dispatchEvent(new WheelEvent('wheel',{deltaY:-400,bubbles:true}));h.scrollTop=Math.max(0,h.scrollTop-400);h.dispatchEvent(new Event('scroll'));for(let i=0;i<8;i++)await new Promise(requestAnimationFrame);});
    const start=await sample(),mark=await page.evaluate(()=>window.backEvidence.states.length),traceMark=fixture.trace.length;
    await page.screenshot({path:output+'/chat-'+owner+'-'+label+'.png'});
    await page.goBack();await page.waitForFunction(()=>document.querySelector('#app').classList.contains('show-directory'));
    const end=await sample();assert(end.directoryVisible&&!end.chatVisible,'Browser Back shows the actual directory');const states=await page.evaluate(mark=>backEvidence.states.slice(mark),mark),requests=fixture.trace.slice(traceMark);
    const switchedToPot=end.selected===key('main')||states.some(state=>state.selected===key('main'));
    results.push({kind:'browser-back-old-pot-directory',owner,parent,start,end,states,requests,switchedToPot});
    if(baseline)assert(switchedToPot,'Baseline must expose the real unwanted Pot selection');
    else{assert.equal(end.selected,key(owner));assert(!switchedToPot,'Directory Back never mounts Pot');assert(!requests.some(row=>row.path==='/api/v1/events'&&row.owner==='main'),'Directory Back retains the active session');await committed(owner,'Reading draft '+owner);}
    await page.screenshot({path:output+'/back-'+owner+'-'+label+'.png'});await capture('browser-back-'+owner);
    const refresh=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/v1/assistants');await context.request.get(fixture.origin+'/fixture/refresh');await refresh;await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.equal(await directory(),true);assert.equal(await selected(),key(baseline?'main':owner));results.push({kind:'directory-refresh-after-back',owner,state:await sample()});
    await page.goForward();await ready();assert.equal(await selected(),key(owner));
    await page.goBack();await page.waitForFunction(()=>document.querySelector('#app').classList.contains('show-directory'));
    await open(owner);assert.equal(await page.locator('#pot-composer').inputValue(),'Reading draft '+owner);
    const restored=await sample();results.push({kind:'reading-roundtrip',owner,start,restored});
    if(!baseline){assert.equal(restored.anchor,start.anchor);assert(Math.abs(restored.offset-start.offset)<=3,'Directory roundtrip keeps the reading anchor within 3px');}
  }
  // Read the actual committed startup identity after Back. Selection and
  // persistence have separate fences; do not assume the wrong owner was saved.
  await startDirectory();await open('beta');await page.locator('#pot-composer').fill('Reopen beta draft');await page.locator('#pot-composer').blur();await committed('beta','Reopen beta draft');await page.goBack();await page.waitForFunction(()=>document.querySelector('#app').classList.contains('show-directory'));
  const startupAfterBack=await page.evaluate(async()=>{const {createIndexedStore,idbRequest}=await import('/state/indexed-store.mjs'),store=createIndexedStore();try{return await store.transaction(['startup'],'readonly',tx=>idbRequest(tx.objectStore('startup').get('last')));}finally{store.close();}});
  assert.equal(startupAfterBack.runtime_id,'back-control');assert.equal(startupAfterBack.boot_id,'back-boot');assert.equal(startupAfterBack.space,'local');
  if(!baseline)assert.equal(startupAfterBack.owner,'beta','Directory Back must not pollute persisted startup scope');
  await page.reload();await page.locator('.assistant-row.is-selected').waitFor({state:'attached'});if(baseline)await ready();const reopened=await sample();results.push({kind:'browser-back-reopen',startupAfterBack,state:reopened});
  if(baseline){assert.equal(reopened.selected,key(startupAfterBack.owner));assert.equal(reopened.directory,false);}else{assert.equal(reopened.selected,key('beta'));assert(reopened.directory&&reopened.directoryVisible&&!reopened.chatVisible);}
  await page.screenshot({path:output+'/directory-reopen-'+label+'.png'});await capture('directory-reopen');
  // A stale conversation URL does not override a later directory entry and
  // its retained owner when the tab is reopened from the startup cache.
  await setup();await page.goto(fixture.origin+'/?conversation=beta&assistant_id=beta&space_id=local&locale=en&theme=light');await ready();assert.equal(await selected(),key('beta'));
  await page.locator('.titlebar .narrow-navigation').tap();await open('gamma');await page.locator('#pot-composer').fill('Linked URL gamma draft');await page.locator('#pot-composer').blur();await committed('gamma','Linked URL gamma draft');await page.locator('.titlebar .narrow-navigation').tap();
  await page.reload();await page.locator('.assistant-row.is-selected').waitFor({state:'attached'});if(baseline)await ready();const staleURL=await sample();results.push({kind:'reload-directory-stale-url',state:staleURL});
  if(!baseline){assert(staleURL.directory&&staleURL.directoryVisible&&!staleURL.chatVisible);assert.equal(staleURL.selected,key('gamma'));assert(!(await page.evaluate(()=>backEvidence.history)).some(event=>event.method==='pushState'),'Reload restores the existing directory entry');await open('gamma');await committed('gamma','Linked URL gamma draft');}
  await capture('stale-url');
  // Explicit conversation entries still restore their own assistant scope.
  await startDirectory();await page.setViewportSize({width:1200,height:852});await open('beta');await open('gamma');await page.setViewportSize({width:390,height:852});
  await page.goBack();await ready();assert.equal(await selected(),key('beta'));assert.equal(await directory(),false);results.push({kind:'explicit-chat-history',state:await sample()});await capture('explicit-chat');
  assert.equal(errors.length,0,errors.join('\n'));assert(!fixture.trace.some(row=>row.path.startsWith('/api/')&&row.method!=='GET'),'Navigation never sends an ordinary draft');
}catch(error){
  results.push({kind:'failure',message:error.message,stack:error.stack});await capture('failure').catch(()=>{});await page?.screenshot({path:output+'/failure-'+label+'.png'}).catch(()=>{});throw error;
}finally{
  await writeFile(output+'/results-'+label+'.json',JSON.stringify({baseline,browserVersion:await browser.version(),viewport:{width:390,height:852},origins:{source:source.origin,fixture:fixture.origin},results,errors,consoleErrors,trace:fixture.trace},null,2));
  await context?.close();await browser.close();await fixture.close();await source.close();
}
console.log(JSON.stringify({label,scenarios:results.length,errors,browserClosed:true,fixturesClosed:true}));
