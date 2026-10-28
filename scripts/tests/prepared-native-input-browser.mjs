import assert from 'node:assert/strict';

// Observe the product's readiness publication in a MutationObserver microtask.
// A RAF poll or a refocusing locator click would skip the native input boundary.
export async function preparedNativeInputReady(browser,origin){
  const results=[];
  for(const focused of [true,false]){
    const context=await browser.newContext({viewport:{width:1280,height:900}}),page=await context.newPage(),errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    try{
      await page.addInitScript(()=>{
        if(window!==top){
          function geometry(){const button=document.querySelector('#inside');if(button){const box=button.getBoundingClientRect();parent.postMessage({type:'fixture-input-geometry',x:box.x+box.width/2,y:box.y+box.height/2,width:innerWidth,height:innerHeight,token:new URL(location.href).searchParams.get('token'),epoch:performance.timeOrigin+performance.now()},'*');}requestAnimationFrame(geometry);}requestAnimationFrame(geometry);
          for(const type of ['pointerdown','pointerup','click'])addEventListener(type,event=>parent.postMessage({type:'fixture-input-return',event:type,viewport:{width:innerWidth,height:innerHeight},trusted:event.isTrusted,target:event.target.id,token:new URL(location.href).searchParams.get('token'),epoch:performance.timeOrigin+performance.now()},'*'),true);
          return;
        }
        let frame=0;const evidence=window.nativeInputReturn={inputs:[],frames:[],points:[]};
        const snapshot=window.nativeInputSnapshot=()=>{
          const iframe=document.querySelector('.history:not([data-preparation-surface]) iframe'),box=iframe?.getBoundingClientRect();
          return {epoch:performance.timeOrigin+performance.now(),frame,stage:!!iframe?.nativePreparation,presenting:!!iframe?.nativePresentation,ready:window.nativeCase?.ready,surfaces:document.querySelectorAll('[data-preparation-surface]').length,holds:document.querySelectorAll('[data-native-preparation-placeholder]').length,focus:document.activeElement===iframe,active:document.activeElement.id||document.activeElement.localName,token:iframe?.src&&new URL(iframe.src).searchParams.get('token'),rect:box&&{x:box.x,y:box.y,width:box.width,height:box.height},clientWidth:iframe?.clientWidth,clientHeight:iframe?.clientHeight};
        };
        addEventListener('message',event=>{const live=event.source===document.querySelector('.history:not([data-preparation-surface]) iframe')?.contentWindow;if(event.data?.type==='fixture-input-geometry'&&live)evidence.points.push({...event.data,received:performance.timeOrigin+performance.now()});if(event.data?.type==='fixture-input-return')evidence.inputs.push({...event.data,live,parent:snapshot()});});
        for(const type of ['pointerdown','pointerup','click'])addEventListener(type,event=>evidence.inputs.push({type:'host',event:type,target:event.target.id||event.target.localName,trusted:event.isTrusted,...snapshot()}),true);
        function tick(){frame++;evidence.frames.push(snapshot());requestAnimationFrame(tick);}requestAnimationFrame(tick);
      });
      await page.goto(origin+'/scripts/tests/fixtures/prepared-focus.html?input-return');
      await page.waitForFunction(()=>nativeCase.ready);
      const child=page.frameLocator('.history:not([data-preparation-surface]) iframe');
      await child.getByRole('textbox',{name:'State value'}).click();await page.keyboard.type('width retained state');await page.keyboard.press('Enter');
      const before=await page.evaluate(()=>nativeCase.messages.findLast(message=>message.label==='keydown'&&message.key==='Enter'));
      assert.equal(before.state,18);if(!focused)await page.locator('#after').click();
      await page.evaluate(()=>{
        let sawStage=false,done=false,resolve;window.nativeInputReturned=new Promise(ready=>resolve=ready);
        const observer=new MutationObserver(()=>queueMicrotask(check));
        function check(){const state=nativeInputSnapshot();sawStage||=state.stage;if(!done&&sawStage&&!state.stage&&state.ready){done=true;observer.disconnect();nativeInputReturn.boundary=state;nativeInputReturn.readyPoint=nativeInputReturn.points.findLast(point=>point.epoch>=nativeInputReturn.started&&point.width===state.clientWidth&&point.height===state.clientHeight&&point.token===state.token&&point.received<=state.epoch);resolve(state);}}
        observer.observe(document,{subtree:true,childList:true,attributes:true});nativeInputReturn.started=performance.timeOrigin+performance.now();nativeCase.start();check();
      });
      // An early stage may still expose an intermediate child viewport. Use
      // geometry already observed before first ready; never wait afterward for
      // an ACK, RAF or a point to arrive, and never refocus the returning host.
      const {boundary,point}=await page.evaluate(async()=>{const boundary=await nativeInputReturned;return {boundary,point:nativeInputReturn.readyPoint};});
      assert(point&&point.epoch<=boundary.epoch&&point.received<=boundary.epoch,'A matching actual child point must exist before first ready');
      assert(boundary.ready&&!boundary.stage&&boundary.surfaces===0);assert.equal(boundary.clientWidth,point.width);assert.equal(boundary.focus,focused);
      assert(!boundary.presenting&&boundary.holds===0,'Ready cannot precede native presentation or leave its loading hold');
      assert(boundary.rect.y+point.y>=0&&boundary.rect.y+point.y<900);
      await page.mouse.click(boundary.rect.x+point.x,boundary.rect.y+point.y);
      await page.evaluate(async()=>{await new Promise(requestAnimationFrame);});
      const evidence=await page.evaluate(()=>nativeInputReturn),click=evidence.inputs.findLast(event=>event.type==='fixture-input-return'&&event.event==='click'&&event.target==='inside'),nextFrame=evidence.frames.find(frame=>frame.epoch>boundary.epoch);
      assert(click?.trusted&&click.live,'First returned trusted click must reach the original child');assert.equal(click.token,boundary.token);
      assert(!nextFrame||click.epoch<nextFrame.epoch,'Input must reach the child before the first RAF after readiness');
      const first=evidence.inputs.filter(event=>event.type==='fixture-input-return'&&event.epoch>=boundary.epoch).slice(0,3);
      assert.deepEqual(first.map(event=>event.event),['pointerdown','pointerup','click']);
      assert(first.every(event=>event.trusted&&event.live&&event.target==='inside'&&event.viewport.width===boundary.clientWidth&&event.viewport.height===boundary.clientHeight&&(!nextFrame||event.epoch<nextFrame.epoch)),'Every first trusted event reaches the current child viewport before next RAF');
      assert.equal(evidence.inputs.filter(event=>event.type==='host'&&event.target==='iframe'&&event.epoch>=boundary.epoch).length,0);
      await page.evaluate(()=>nativeCase.probe('after-input-return'));await page.waitForFunction(()=>nativeCase.messages.some(message=>message.label==='after-input-return'));
      const probe=await page.evaluate(()=>nativeCase.messages.findLast(message=>message.label==='after-input-return'));
      assert.equal(probe.documentID,before.documentID);assert.equal(probe.token,before.token);assert.equal(probe.value,'width retained state');assert.equal(probe.state,18);assert.equal(probe.internalFocus,'inside');
      assert(await page.evaluate(()=>originalNative===document.querySelector('.history:not([data-preparation-surface]) iframe')));
      const retired=await page.evaluate(()=>nativeCase.close());assert.equal(retired.loads,0);assert.equal(retired.acquired,retired.released);assert.deepEqual(retired.errors,[]);assert.deepEqual(errors,[]);
      results.push({focused,before,boundary,point,probe,evidence,acquired:retired.acquired,released:retired.released,loads:retired.loads});
    }finally{await context.close();}
  }
  return results;
}

export async function preparedNativeUnfocusedTab(browser,origin){
  const context=await browser.newContext({viewport:{width:1280,height:900}}),page=await context.newPage(),errors=[],tabs=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
    await page.goto(origin+'/scripts/tests/fixtures/prepared-focus.html');await page.waitForFunction(()=>nativeCase.ready);
    const child=page.frameLocator('.history:not([data-preparation-surface]) iframe');
    await child.getByRole('textbox',{name:'State value'}).click();await page.keyboard.type('unfocused retained state');await page.keyboard.press('Enter');
    const before=await page.evaluate(()=>nativeCase.messages.findLast(message=>message.label==='keydown'&&message.key==='Enter'));
    await page.locator('#after').click();await page.evaluate(()=>nativeCase.start());await page.waitForFunction(()=>nativeCase.staged);
    await page.locator('#before').click();
    for(let index=0;index<10;index++){
      await page.keyboard.press('Tab');const snapshot=await page.evaluate(()=>nativeCase.snapshot('unfocused-tab'));
      tabs.push(snapshot);assert(!snapshot.outerFocus&&!snapshot.hiddenFocus&&snapshot.allSurfacesInert,'Tab must skip the hidden native document and all new candidates');
      if(snapshot.active==='after')break;
    }
    assert.equal(tabs.at(-1).active,'after');
    await page.evaluate(()=>nativeCase.release());await page.waitForFunction(()=>nativeCase.ready);
    assert.equal(await page.evaluate(()=>document.activeElement.id),'after');
    await page.evaluate(()=>nativeCase.probe('after-unfocused-tab'));await page.waitForFunction(()=>nativeCase.messages.some(message=>message.label==='after-unfocused-tab'));
    const probe=await page.evaluate(()=>nativeCase.messages.findLast(message=>message.label==='after-unfocused-tab'));
    assert.equal(probe.token,before.token);assert.equal(probe.documentID,before.documentID);assert.equal(probe.value,before.value);assert.equal(probe.state,before.state);
    const retired=await page.evaluate(()=>nativeCase.close());assert.equal(retired.loads,0);assert.equal(retired.acquired,retired.released);assert.deepEqual(retired.errors,[]);assert.deepEqual(errors,[]);
    return {before,tabs,probe,acquired:retired.acquired,released:retired.released,loads:retired.loads};
  }finally{await context.close();}
}

export async function preparedNativePresentationFences(browser,origin){
  const results=[];
  for(const action of ['resize','retire']){
    const context=await browser.newContext({viewport:{width:1280,height:900}}),page=await context.newPage(),errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    try{
      await page.goto(origin+'/scripts/tests/fixtures/prepared-focus.html?input-return');await page.waitForFunction(()=>nativeCase.ready);
      await page.locator('#after').click();
      await page.evaluate(()=>{
        let resolve;window.nativePresentationPending=new Promise(ready=>resolve=ready);
        const observer=new MutationObserver(()=>{
          const iframe=document.querySelector('.history:not([data-preparation-surface]) iframe');
          if(iframe?.nativePresentation&&!nativeCase.ready){observer.disconnect();resolve({ready:nativeCase.ready,focus:document.activeElement.id,token:new URL(iframe.src).searchParams.get('token')});}
        });observer.observe(document,{subtree:true,attributes:true,childList:true});nativeCase.start();
      });
      const pending=await page.evaluate(()=>nativePresentationPending);assert(!pending.ready&&pending.focus==='after');
      assert(await page.evaluate(()=>!!originalNative.nativePresentation),'Exercise the actual return/presentation boundary');
      let retired;
      if(action==='resize'){
        await page.evaluate(()=>nativeCase.resize());await page.waitForFunction(()=>nativeCase.ready);
        assert.equal(await page.evaluate(()=>new URL(originalNative.src).searchParams.get('token')),pending.token);
        assert(await page.evaluate(()=>!originalNative.nativePresentation&&!originalNative.nativePreparation&&!originalNative.hasAttribute('aria-hidden')&&!originalNative.hasAttribute('tabindex')));
        retired=await page.evaluate(()=>nativeCase.close());
      }else retired=await page.evaluate(()=>nativeCase.close());
      await page.evaluate(async()=>{await new Promise(requestAnimationFrame);await new Promise(requestAnimationFrame);});
      assert.equal(await page.evaluate(()=>document.activeElement.id),'after');
      assert(await page.evaluate(()=>!originalNative.nativePresentation&&!document.querySelector('[data-native-preparation-placeholder]')));
      assert.equal(retired.loads,0);assert.equal(retired.acquired,retired.released);assert.deepEqual(retired.errors,[]);assert.deepEqual(errors,[]);
      results.push({action,pending,acquired:retired.acquired,released:retired.released,loads:retired.loads});
    }finally{await context.close();}
  }
  return results;
}
