// Focused native-scroll regression against TestResourceBrowserFixture.
// Uses the existing browser harness; CUA acceptance remains a separate check.
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {launchBrowser} from './browser-harness.mjs';

const [origin,beforeOrigin,captures='output/pr-images/html-scroll']=process.argv.slice(2);
if(!origin)throw Error('Pass the isolated resource fixture origin');
const browser=await launchBrowser(),evidence=[];
const sample=page=>page.evaluate(()=>window.htmlScrollFixture.metrics());
const settled=page=>page.waitForTimeout(180); // Native wheel/compositor delivery.
async function mount(page,kind,legacy=false){
  await page.evaluate(([kind,legacy])=>window.htmlScrollFixture.mount(kind,legacy),[kind,legacy]);
  await page.waitForFunction(()=>document.querySelector('#message pot-resource')?.dataset.state==='ready');
  await settled(page);
}
async function wheel(page,target,delta){
  const box=await target.boundingBox();assert(box,'Scroll target is mounted');
  await page.mouse.move(box.x+Math.min(box.width-10,box.width/2),box.y+Math.min(box.height-10,box.height/2));
  await page.mouse.wheel(0,delta);await settled(page);
}
try{
  await mkdir(captures,{recursive:true});
  for(const [width,theme] of [[1280,'light'],[390,'dark']]){
    const context=await browser.newContext({viewport:{width,height:900},reducedMotion:'reduce'}),page=await context.newPage(),errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    await page.goto(`${origin}/scripts/tests/fixtures/html-scroll.html?theme=${theme}`);
    await page.waitForFunction(()=>window.fixtureReady);
    for(const legacy of [false,true])for(const kind of ['long','nested','short']){
      await mount(page,kind,legacy);
      const frame=page.frameLocator('#message iframe'),target=kind==='nested'?frame.locator('#list'):page.locator('#message iframe');
      const initial=await sample(page);assert(initial.chat>0&&initial.chat<initial.chatMax,'Chat starts in the middle');
      assert.equal(initial.sandbox,'allow-scripts');
      // Use a visible area inside the frame, never its surrounding resource card.
      await page.locator('#message iframe').scrollIntoViewIfNeeded();
      const start=await sample(page),frameBox=await page.locator('#message iframe').boundingBox();
      if(kind==='short'){
        await page.mouse.move(frameBox.x+frameBox.width/2,frameBox.y+frameBox.height/2);await page.mouse.wheel(0,120);await settled(page);
        assert((await sample(page)).chat>start.chat,'Short content retains ordinary chat handoff');
        evidence.push({width,theme,legacy,kind,start:start.chat,end:(await sample(page)).chat});continue;
      }
      // Read actual browser geometry to put the target under the pointer.
      await wheel(page,target,240);
      let inside=JSON.parse((await sample(page)).frame);assert((kind==='nested'?inside.nested:inside.root)>0,`Native wheel scrolls inside: ${JSON.stringify({width,theme,kind,legacy,start,inside})}`);
      await frame.locator('#bottom').click();await settled(page);
      const atBottom=await sample(page);inside=JSON.parse(atBottom.frame);assert.equal(kind==='nested'?inside.nested:inside.root,inside.max);
      await wheel(page,target,360);await wheel(page,target,360);
      assert.equal((await sample(page)).chat,atBottom.chat,`Bottom boundary cannot chain to chat: ${JSON.stringify({width,theme,legacy,kind,atBottom,after:await sample(page),box:await target.boundingBox()})}`);
      await frame.locator('#top').click();await settled(page);const atTop=await sample(page);
      await wheel(page,target,-360);await wheel(page,target,-360);
      assert.equal((await sample(page)).chat,atTop.chat,'Top boundary cannot chain to chat');
      assert.equal(JSON.parse((await sample(page)).frame).isolated,true,'Parent DOM remains inaccessible');
      await frame.locator('#action').click();assert.equal(JSON.parse((await sample(page)).frame).interactions,1);
      await frame.locator('input').fill('Edited safely');assert.equal(await frame.locator('input').inputValue(),'Edited safely');
      if(kind==='nested'){
        await target.focus();await page.keyboard.press('ArrowDown');await settled(page);
        assert(JSON.parse((await sample(page)).frame).nested>0,'Native keyboard list scrolling survives');
      }
      // Move directly outside the iframe: no click or focus exit.
      const chatBox=await page.locator('.history:not([data-preparation-surface])').boundingBox(),beforeOutside=(await sample(page)).chat;
      await page.mouse.move(chatBox.x+3,Math.min(850,chatBox.y+80));await page.mouse.wheel(0,100);await settled(page);
      assert((await sample(page)).chat>beforeOutside,'Moving outside resumes chat without a click');
      evidence.push({width,theme,legacy,kind,bottom:atBottom.chat,top:atTop.chat,outside:(await sample(page)).chat});
    }
    await mount(page,'long');
    const link=page.getByRole('link',{name:'Open HTML viewer'});await link.scrollIntoViewIfNeeded();
    const original=await sample(page);
    assert(original.pageMax>0,'The page itself is also scrollable beneath the modal');
    for(const closeBy of ['button','Escape','button']){
      await link.click();await page.locator('dialog pot-resource[data-state=ready]').waitFor();
      assert.equal((await sample(page)).locked,'hidden');
      const frame=page.frameLocator('dialog iframe');await wheel(page,page.locator('dialog iframe'),260);
      assert(JSON.parse((await sample(page)).frame).root>0,'Modal keeps internal scrolling');
      await frame.locator('#bottom').click();await wheel(page,page.locator('dialog iframe'),500);
      const locked=await sample(page);assert.equal(locked.chat,original.chat);assert.equal(locked.page,original.page);
      // Backdrop and header also cannot scroll the underlying page.
      await page.mouse.move(2,400);await page.mouse.wheel(0,500);await settled(page);assert.equal((await sample(page)).chat,original.chat);
      if(closeBy==='Escape'){
        // Neither an unrelated window nor this frame with a bad token may close
        // the viewer. Author-handled Escape must also remain with the content.
        await page.evaluate(()=>postMessage({type:'pot-resource',token:'wrong',action:'escape'},'*'));
        await frame.locator('body').evaluate(()=>parent.postMessage({type:'pot-resource',token:'wrong',action:'escape'},'*'));
        await frame.locator('input').focus();
        await frame.locator('body').evaluate(()=>window.addEventListener('keydown',event=>{if(event.key==='Escape')event.preventDefault();},{once:true}));
        await page.keyboard.press('Escape');await settled(page);assert.equal(await page.locator('dialog[open]').count(),1,'Author-handled Escape remains in the content');
      }
      if(closeBy==='button')await page.getByRole('button',{name:'Close',exact:true}).click();else await page.keyboard.press('Escape');
      await page.locator('dialog').waitFor({state:'detached'});
      assert.equal((await sample(page)).chat,original.chat);assert.equal((await sample(page)).page,original.page);
      assert.equal((await sample(page)).locked,'');assert(await link.evaluate(element=>document.activeElement===element),'Focus returns to opener');
    }
    const chatBox=await page.locator('.history:not([data-preparation-surface])').boundingBox();
    await page.mouse.move(chatBox.x+3,chatBox.y+80);await page.mouse.wheel(0,100);await settled(page);
    assert((await sample(page)).chat>original.chat,'Chat scrolling resumes after the modal closes');
    await page.mouse.move(2,400);await page.mouse.wheel(0,120);await settled(page);
    assert((await sample(page)).page>original.page,'Page scrolling resumes after the modal closes');
    await page.evaluate(()=>document.scrollingElement.scrollTop=0);
    await mount(page,'long');await link.click();await page.locator('dialog pot-resource[data-state=ready]').waitFor();
    await page.evaluate(()=>document.querySelector('#inline').replaceChildren());
    await page.locator('dialog').waitFor({state:'detached'});
    assert.equal((await sample(page)).locked,'','Unmounting the opener also releases the lock');
    evidence.push({width,theme,modal:original.chat,close:'button / Escape / reopen',focus:true});
    assert.deepEqual(errors,[]);
    await context.close();
    // Identical baseline/after source fixture and native boundary gesture.
    for(const [version,base] of [['before',beforeOrigin],['after',origin]]){
      if(!base)continue;
      const shot=await browser.newPage({viewport:{width,height:900},reducedMotion:'reduce'});
      await shot.goto(`${base}/scripts/tests/fixtures/html-scroll.html?theme=${theme}`);await shot.waitForFunction(()=>window.fixtureReady);await mount(shot,'long');
      await shot.locator('#message iframe').scrollIntoViewIfNeeded();
      const frame=shot.frameLocator('#message iframe');await frame.locator('#bottom').click();await settled(shot);
      const initial=(await sample(shot)).chat;await wheel(shot,shot.locator('#message iframe'),180);
      const result=await sample(shot);if(version==='after')assert.equal(result.chat,initial);else assert(result.chat>initial,'The main baseline reproduces scroll chaining');
      await settled(shot);await shot.screenshot({path:`${captures}/html-boundary-${width}-${theme}-${version}.png`});
      evidence.push({capture:version,width,initial,actual:result.chat,frame:JSON.parse(result.frame)});await shot.close();
    }
  }
  console.log(JSON.stringify(evidence,null,2));
}catch(error){console.error('Completed scenarios:',JSON.stringify(evidence));throw error;}finally{await browser.close();}
