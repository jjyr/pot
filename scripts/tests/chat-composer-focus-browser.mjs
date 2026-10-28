import assert from 'node:assert/strict';
import {sourceServer,launchBrowser,isolatedPage} from './browser-harness.mjs';
import {waitForOutbox} from './outbox-persistence-browser.mjs';
import {chatInteractionsFixture} from './chat-interactions-fixture.mjs';

const waitFor=async(predicate,timeout=5000,label='condition')=>{
  const started=Date.now();
  while(Date.now()-started<timeout){
    if(await predicate())return;
    await new Promise(resolve=>setTimeout(resolve,50));
  }
  throw Error('Timed out waiting for '+label);
};

const source=await sourceServer();
let fixture,browser,failure;
try{
  fixture=await chatInteractionsFixture(source.origin);
  browser=await launchBrowser();
  await isolatedPage(browser,fixture.origin,async page=>{
    await page.addInitScript(()=>{
      const log=[];
      const targetOf=node=>node?.classList?.contains?.('composer-input')?'composer-input':node?.classList?.contains?.('send-button')?'send-button':(node?.className?.baseVal||node?.className||node?.tagName||'');
      const push=(kind,node,extra={})=>log.push({kind,target:targetOf(node),...extra,at:Math.round(performance.now())});
      for(const type of ['focus','blur','focusin','focusout'])window.addEventListener(type,event=>push(type,event.target),true);
      for(const type of ['compositionstart','compositionend'])window.addEventListener(type,event=>push(type,event.target),true);
      const blur=HTMLTextAreaElement.prototype.blur;
      HTMLTextAreaElement.prototype.blur=function(){push('explicit-blur',this);return blur.call(this);};
      const changes=records=>{for(const record of records)if(record.type==='attributes'&&record.attributeName==='disabled')push('disabled',record.target,{value:record.target.disabled});};
      const observe=()=>new MutationObserver(changes).observe(document.documentElement,{attributes:true,attributeFilter:['disabled'],subtree:true});
      if(document.documentElement)observe();else document.addEventListener('DOMContentLoaded',observe,{once:true});
      window.__focusMutations=log;
    });
    const composerState=()=>page.evaluate(()=>{
      const input=document.querySelector('.composer-input');
      return {focused:document.activeElement===input,value:input?.value??'',selected:{start:input?.selectionStart??-1,end:input?.selectionEnd??-1}};
    });
    const control=body=>fetch(fixture.origin+'/fixture/control',{method:'POST',body:JSON.stringify(body)}).then(response=>response.json());
    const explicitBlurs=()=>page.evaluate(()=>window.__focusMutations.filter(entry=>entry.kind==='explicit-blur'&&entry.target==='composer-input').length);
    const streamCloses=()=>fixture.trace.filter(entry=>entry.kind==='sse-close'&&entry.path==='/api/v1/events'&&entry.owner==='alpha').length;
    const messagePosts=()=>fixture.trace.filter(entry=>entry.kind==='http-start'&&entry.path==='/api/v1/messages').length;
    try{
      await page.goto(fixture.origin+'/?assistant_id=alpha');
      await page.waitForSelector('.assistant-row',{timeout:20000});
      if(!(await page.locator('.assistant-row.is-selected').count())){
        await page.locator('.assistant-row',{hasText:'Synthetic alpha'}).first().click();
      }
      await page.waitForFunction(()=>{const input=document.querySelector('.composer-input');return input&&!input.disabled&&document.querySelector('.assistant-row.is-selected');},null,{timeout:20000});
      await page.locator('.composer-input').click();
      await page.keyboard.type('focus retention');
      await page.waitForFunction(()=>document.querySelector('.composer-input')?.value==='focus retention');
      await page.evaluate(()=>document.querySelector('.composer-input').setSelectionRange(2,7));
      const beforeSameTarget=await composerState();
      assert.equal(beforeSameTarget.focused,true);
      assert.deepEqual(beforeSameTarget.selected,{start:2,end:7});

      await page.evaluate(()=>{window.__composerNode=document.querySelector('.composer-input');window.__sendNode=document.querySelector('.send-button');window.__sendIcons=[...__sendNode.children];});
      const closesBefore=streamCloses();
      await page.evaluate(()=>document.querySelector('.assistant-row.is-selected').click());
      await page.waitForTimeout(300);
      const afterSameTarget=await composerState();
      assert.equal(afterSameTarget.focused,true,'same-target directory click must not blur the composer');
      assert.equal(afterSameTarget.value,'focus retention','same-target directory click must not reset the draft');
      assert.deepEqual(afterSameTarget.selected,{start:2,end:7},'same-target directory click must preserve the selection');
      assert.equal(streamCloses(),closesBefore,'same-target directory click must not retire the chat subscription');
      assert.equal(await explicitBlurs(),0,'same-target directory click must not call composer.blur()');

      await page.evaluate(()=>{window.__focusMutations.length=0;});
      await page.evaluate(()=>{
        const input=document.querySelector('.composer-input');
        input.focus();
        input.value='ime draft';
        input.setSelectionRange(2,5);
        input.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true,data:''}));
        input.dispatchEvent(new InputEvent('input',{bubbles:true,data:'ime draft',inputType:'insertCompositionText',isComposing:true}));
      });
      await control({holdDirectory:true});
      await control({directory:true,background:true});
      await page.waitForTimeout(150);
      const duringRefresh=await composerState();
      assert.equal(duringRefresh.focused,true,'background refresh must not blur the composing composer');
      assert.equal(duringRefresh.value,'ime draft','background refresh must not replace composing text');
      assert.deepEqual(duringRefresh.selected,{start:2,end:5},'background refresh must preserve the composing selection');
      await control({holdDirectory:false});
      await page.waitForTimeout(300);
      const afterRefresh=await composerState();
      assert.equal(afterRefresh.focused,true);
      assert.equal(afterRefresh.value,'ime draft');
      assert.deepEqual(afterRefresh.selected,{start:2,end:5});
      await page.evaluate(()=>document.querySelector('.composer-input').dispatchEvent(new CompositionEvent('compositionend',{bubbles:true,data:'ime draft'})));
      await page.waitForTimeout(120);

      await page.context().setOffline(true);
      const offlinePosts=messagePosts();
      assert.equal(await page.locator('.send-button').isEnabled(),true,'offline input remains locally submittable');
      await page.keyboard.press('Enter');
      await waitForOutbox(page,([rows])=>rows.some(row=>row.request.text==='ime draft'&&row.state==='queued'));
      assert.equal(messagePosts(),offlinePosts,'offline local submission does not POST');
      assert.equal((await composerState()).focused,true,'offline local submission retains focus');
      await page.context().setOffline(false);
      await waitForOutbox(page,([rows])=>rows.some(row=>row.request.text==='ime draft'&&row.state==='accepted'));
      await page.locator('.composer-input').fill('final draft');
      await page.waitForFunction(()=>!document.querySelector('.send-button').disabled);
      const messagesBefore=messagePosts();
      await Promise.all([page.keyboard.press('Enter'),control({background:true,directory:true})]);
      await waitFor(()=>messagePosts()>messagesBefore,5000,'submitted message');
      await page.waitForFunction(()=>document.querySelector('.composer-input')?.value==='');
      assert(await page.evaluate(()=>document.querySelector('.composer-input')===__composerNode&&document.querySelector('.send-button')===__sendNode&&__sendIcons.every((node,index)=>node===__sendNode.children[index])),'composer, button and mode icons retain node identity');
      const afterSend=await composerState();
      assert.equal(afterSend.focused,true,'submitting must leave focus in the composer');
      assert.equal(await explicitBlurs(),0,'submitting must not call composer.blur()');
      const disabledMutations=await page.evaluate(()=>window.__focusMutations.filter(entry=>entry.kind==='disabled'&&entry.target==='send-button').map(entry=>entry.value));
      assert.ok(disabledMutations.includes(true),'send is disabled only during a local commit or for an empty draft');
      assert.ok(disabledMutations.includes(false),'valid draft re-enables send');
      assert.equal(await page.evaluate(()=>window.__focusMutations.some(entry=>entry.kind==='disabled'&&entry.target==='composer-input')),false,'the composer textarea must never be disabled while a draft is present');
    }catch(error){
      const log=await page.evaluate(()=>window.__focusMutations).catch(()=>[]);
      throw Error(error.message+'\ncomposer mutation log: '+JSON.stringify(log));
    }
  });
}catch(error){failure=error;}
finally{
  if(browser)await browser.close().catch(()=>{});
  if(fixture)await fixture.close().catch(()=>{});
  await source.close();
}
if(failure)throw failure;
console.log('PASS chat composer focus, selection, IME and send regression');
