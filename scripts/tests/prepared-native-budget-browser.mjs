import assert from 'node:assert/strict';

// Change only the original child DOM. Its ordinary ResizeObserver must drive
// real pot.resize messages while the outer width and document identity stay put.
export async function preparedNativeResizeBudget(browser,origin){
  const context=await browser.newContext({viewport:{width:1280,height:900}}),page=await context.newPage(),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
    await page.addInitScript(()=>{
      if(window!==top){
        addEventListener('message',event=>{
          if(event.data?.type!=='fixture-budget-oscillate')return;
          const extra=document.createElement('div');document.body.append(extra);
          const start=performance.now();let index=0;
          function tick(){
            extra.style.height=(index++%2?440:240)+'px';
            if(performance.now()-start<5400)requestAnimationFrame(tick);
            else{extra.remove();parent.postMessage({type:'fixture-budget-ended'},'*');}
          }
          tick();
        });return;
      }
      const evidence=window.nativeBudget={trace:[]};
      const snapshot=()=>{
        const iframe=document.querySelector('.history:not([data-preparation-surface]) iframe'),owner=iframe?.closest('pot-resource'),box=owner?.getBoundingClientRect();
        return {at:performance.now(),ready:window.nativeCase?.ready,deadline:owner?.nativeReflowDeadline,revision:owner?.nativeResizeRevision,
          stage:!!iframe?.nativePreparation,presentation:!!iframe?.nativePresentation,fallback:owner?.dataset.preparedFallback,
          frame:owner?.classList.contains('prepared-frame'),height:box?.height,width:box?.width,overflow:owner&&getComputedStyle(owner).overflowY,measured:window.nativeCase?.measured,
          viewport:iframe&&{width:iframe.clientWidth,height:iframe.clientHeight,fixed:iframe.classList.contains('prepared-frame')},outerWidth:document.querySelector('.history:not([data-preparation-surface])')?.getBoundingClientRect().width,
          surfaces:document.querySelectorAll('[data-preparation-surface]').length,holds:document.querySelectorAll('[data-native-preparation-placeholder]').length,
          focus:document.activeElement?.id,token:iframe?.src&&new URL(iframe.src).searchParams.get('token')};
      };
      function tick(){evidence.trace.push(snapshot());requestAnimationFrame(tick);}requestAnimationFrame(tick);
      addEventListener('message',event=>{if(event.data?.type==='fixture-budget-ended')evidence.ended=snapshot();});
      window.startNativeBudget=()=>new Promise(resolve=>{
        const observer=new MutationObserver(()=>{
          const iframe=document.querySelector('.history:not([data-preparation-surface]) iframe');
          if(!iframe?.nativePresentation||nativeCase.ready)return;
          observer.disconnect();evidence.pending=snapshot();window.budgetOwner=iframe.closest('pot-resource');
          iframe.contentWindow.postMessage({type:'fixture-budget-oscillate'},'*');
          // A single observation at the contract boundary, not a readiness retry.
          setTimeout(()=>{evidence.afterBudget=snapshot();resolve(evidence.afterBudget);},5200);
        });observer.observe(document,{subtree:true,childList:true,attributes:true});nativeCase.start();
      });
    });
    await page.goto(origin+'/scripts/tests/fixtures/prepared-focus.html?input-return');await page.waitForFunction(()=>nativeCase.ready);
    await page.evaluate(()=>nativeCase.probe('budget-before'));await page.waitForFunction(()=>nativeCase.messages.some(message=>message.label==='budget-before'));
    const before=await page.evaluate(()=>nativeCase.messages.findLast(message=>message.label==='budget-before'));
    await page.locator('#after').click();
    const afterBudget=await page.evaluate(()=>startNativeBudget());
    const evidence=await page.evaluate(()=>nativeBudget),pending=evidence.pending;
    assert(pending.presentation&&!pending.ready,'Exercise the actual pending presentation');
    assert(afterBudget.at-pending.at>=5200);
    assert(afterBudget.ready&&afterBudget.frame&&afterBudget.fallback==='true','Continuous child RO must finish within the original five-second budget in a fixed frame');
    assert(afterBudget.viewport.fixed,'The original input viewport must have a measured fixed height too');
    assert.equal(afterBudget.overflow,'auto');assert(afterBudget.height>=240);
    assert(!afterBudget.stage&&!afterBudget.presentation&&!afterBudget.holds&&!afterBudget.surfaces);
    assert.equal(afterBudget.measured.registered,afterBudget.measured.actual,'Registered virtual geometry must equal the real fixed-frame row');
    assert.equal(afterBudget.focus,'after');assert.equal(afterBudget.token,pending.token);
    const trace=evidence.trace.filter(sample=>sample.at>=pending.at);
    assert(trace.every(sample=>sample.outerWidth===340),'The bounded case changes only child content after the single outer width change');
    assert(trace.filter(sample=>sample.deadline!==undefined).every(sample=>sample.deadline===pending.deadline),'Commit and RO must not renew the original component deadline');
    assert(afterBudget.revision>pending.revision+10,'Exercise repeated real document resizes');
    const fallback=trace.filter(sample=>sample.fallback==='true'&&!sample.stage&&!sample.presentation);
    assert(fallback.length>1&&fallback.every(sample=>sample.height===afterBudget.height),'Late resizes remain inside the measured fixed frame');
    assert(afterBudget.revision>fallback[0].revision,'Real RO updates continue after the fixed frame is committed');
    const completed=trace.findIndex(sample=>sample.ready&&sample.fallback==='true');
    assert(completed>=0&&trace.slice(completed).every(sample=>sample.ready&&!sample.stage&&!sample.presentation&&!sample.surfaces&&!sample.holds),'Interior-only late resizes cannot reopen preparation after the bounded fallback completes');
    assert(trace.slice(completed).every(sample=>sample.viewport.height===afterBudget.viewport.height),'Late content cannot restart the real native viewport presentation boundary');
    await page.waitForFunction(()=>nativeBudget.ended);
    await page.waitForFunction(()=>nativeCase.ready);
    await page.evaluate(()=>nativeCase.probe('budget-complete'));await page.waitForFunction(()=>nativeCase.messages.some(message=>message.label==='budget-complete'));
    const probe=await page.evaluate(()=>nativeCase.messages.findLast(message=>message.label==='budget-complete'));
    assert.equal(probe.token,pending.token);assert.equal(probe.documentID,before.documentID);
    assert(await page.evaluate(()=>originalNative===document.querySelector('.history:not([data-preparation-surface]) iframe')));
    const retired=await page.evaluate(()=>nativeCase.close());
    assert(await page.evaluate(()=>budgetOwner.nativeReflowDeadline===undefined),'Real retirement ends the component budget');
    assert.equal(retired.loads,0);assert.equal(retired.acquired,retired.released);assert.deepEqual(retired.errors,[]);assert.deepEqual(errors,[]);
    const reopenedAt=await page.evaluate(()=>{const at=performance.now();nativeCase.reopen();return at;});
    await page.waitForFunction(()=>nativeCase.ready);
    const replacement=await page.evaluate(()=>{
      const iframe=document.querySelector('.history:not([data-preparation-surface]) iframe'),owner=iframe.closest('pot-resource');
      return {at:performance.now(),deadline:owner.nativeReflowDeadline,token:new URL(iframe.src).searchParams.get('token'),newOwner:owner!==budgetOwner,oldConnected:budgetOwner.isConnected};
    });
    assert(replacement.newOwner&&!replacement.oldConnected&&replacement.token!==pending.token);
    assert(replacement.deadline>=reopenedAt+5000&&replacement.deadline<=replacement.at+5000,'Only the new instance receives a fresh five-second budget');
    const final=await page.evaluate(()=>nativeCase.close());assert.equal(final.acquired,final.released);assert.equal(final.loads,0);assert.deepEqual(final.errors,[]);assert.deepEqual(errors,[]);
    return {before,pending,afterBudget,probe,reopenedAt,replacement,evidence:await page.evaluate(()=>nativeBudget),acquired:final.acquired,released:final.released,loads:final.loads};
  }catch(error){
    error.evidence=await page.evaluate(()=>({budget:window.nativeBudget,retired:window.nativeCase?.close()})).catch(()=>null);throw error;
  }finally{await context.close();}
}
