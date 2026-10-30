import assert from 'node:assert/strict';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
import {attachmentPickerFixture} from './attachment-picker-fixture.mjs';
const source=await sourceServer({}),fixture=await attachmentPickerFixture(source.origin),browser=await launchBrowser();
try{
  for(const width of [1280,390]){
    const context=await browser.newContext({serviceWorkers:'block',viewport:{width,height:850}}),page=await context.newPage(),errors=[];page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));
    let releaseLinks,linkEntered,renamed=false;const entered=new Promise(resolve=>linkEntered=resolve),held=new Promise(resolve=>releaseLinks=resolve);
    try{
      await context.request.post(fixture.origin+'/fixture/control',{data:{messages:[{id:'cached',role:'assistant',text:'Local-first retained transcript',status:'complete'}],permission:true}});
      await page.goto(fixture.origin+'/?assistant_id=alpha&locale=en');await page.locator('.history[role="log"]:not([data-preparation-surface])').getByText('Local-first retained transcript').waitFor();
      await page.waitForFunction(()=>document.querySelector('.connection-status.is-connected'));
      await page.evaluate(async()=>{const {createIndexedStore,idbRequest}=await import('/state/indexed-store.mjs');const db=createIndexedStore({name:'pot-chat-cache'});try{await db.transaction(['projections'],'readwrite',async tx=>{const store=tx.objectStore('projections');for(const row of await idbRequest(store.getAll()))if(row.meta.path==='/api/v1/agent-links')store.delete(row.key);});}finally{db.close();}});
      await context.route('**/api/v1/agent-links?*',async route=>{linkEntered();await held;await route.continue().catch(()=>{});});
      await context.route('**/api/v1/assistants?*',async route=>{const response=await route.fetch();const items=await response.json();await route.fulfill({response,json:items.map(item=>item.id==='alpha'&&renamed?{...item,name:'Updated cached assistant'}:item)});});
      await page.reload({waitUntil:'domcontentloaded'});await entered;
      await page.locator('.history[role="log"]:not([data-preparation-surface])').getByText('Local-first retained transcript').waitFor();
      const composer=page.locator('#pot-composer');await composer.fill('Local-first draft remains focused');await composer.evaluate(node=>{window.retainedComposer=node;node.focus();node.setSelectionRange(6,10);});
      renamed=true;await context.request.post(fixture.origin+'/fixture/control',{data:{directory:true}});
      await page.getByText('Updated cached assistant',{exact:true}).first().waitFor({state:'attached'});
      assert.deepEqual(await composer.evaluate(node=>({same:node===window.retainedComposer,focused:document.activeElement===node,value:node.value,start:node.selectionStart,end:node.selectionEnd})),{same:true,focused:true,value:'Local-first draft remains focused',start:6,end:10});
      assert(await page.locator('.history[role="log"]:not([data-preparation-surface])').getByText('Local-first retained transcript').isVisible());assert.deepEqual(errors,[]);releaseLinks();
      console.log('PASS warm IndexedDB transcript, missing/held links, independent assistant invalidation and composer focus at '+width+'px');
    }finally{releaseLinks();await context.close();}
  }
}finally{await browser.close();await fixture.close();await source.close();}
