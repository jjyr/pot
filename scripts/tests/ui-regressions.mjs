import {parseArgs} from 'node:util';
import {performance} from 'node:perf_hooks';
import {groups,faults,select,fixtures} from './ui-regression-plan.mjs';
import {sourceServer,launchBrowser,isolatedPage,contract,fixture} from './browser-harness.mjs';
import {taskContinuity,witnessMessages} from './task-continuity-browser.mjs';
import {composerContinuationBrowser} from './composer-continuation-browser.mjs';
import {attachmentPickerBrowser} from './attachment-picker-browser.mjs';
import {chatStreamRecovery} from './chat-stream-recovery-browser.mjs';
import {taskHistoryBrowser,taskHistoryEdges} from './task-history-browser.mjs';
import {referenceCancellation} from './reference-cancel-browser.mjs';
const {values}=parseArgs({options:{group:{type:'string',default:'all'},list:{type:'boolean'},fault:{type:'string'},case:{type:'string'}}});
let selected=select(values.group);
if(values.case){if(!selected.includes(values.case))throw Error('Unknown scenario in selected group: '+values.case);selected=[values.case];}
if(values.fault&&(!faults[values.fault]||values.group!==faults[values.fault].group))throw Error('Select the matching group for a known fault');
if(values.list){console.log(JSON.stringify({group:values.group,scenarios:selected,groups},null,2));}
else {
  const server=await sourceServer({fault:values.fault});let browser;const started=performance.now();let completed=0;
  try {
    browser=await launchBrowser();
    for(const name of selected){
      const start=performance.now();let result;
      console.log('RUN '+name);
      // The directory matrix owns its device contexts. No unused outer context.
      if(name==='reference-cancel'){
        result=[];
        for(const phone of [false,true]){
          const context=await browser.newContext({viewport:phone?{width:390,height:844}:{width:1280,height:800},hasTouch:phone,isMobile:phone});
          try{result.push(await referenceCancellation(await context.newPage(),server.origin));}finally{await context.close();}
        }
      }
      else if(name==='composer-continuation')result=await composerContinuationBrowser(browser,{sourceOrigin:server.origin});
      else if(name==='attachment-picker')result=await attachmentPickerBrowser(browser,{sourceOrigin:server.origin});
      else if(name==='chat-stream-recovery')result=await chatStreamRecovery(browser,{sourceOrigin:server.origin});
      else if(name==='directory')result=await contract('pot-ui-mobile-directory-browser.js',null,{browser,origin:server.origin});
      else if(name==='composer-enter')result=await contract('pot-ui-mobile-enter-browser.js',null,{browser,origin:server.origin,base:server.origin+'/web/src'});
      else result=await isolatedPage(browser,server.origin,async page=>{
        if(name==='machines')return contract('pot-ui-machines-browser.js',page,{origin:server.origin});
        if(/^(assistant-start|first-connection|settings|composer|usage)-/.test(name)||name==='linked-approvals'||name==='patch-approvals')return focused(page,name,server.origin);
        if(name==='controller')return contract('pot-ui-conversation-controller-browser.js',page,{origin:server.origin});
        if(fixtures[name])return fixture(page,server.origin,name,fixtures[name]);
        if(name==='scroll-components')return fixture(page,server.origin,'chat-scroll.html?contract=window','DONE scrolling checks');
        if(name==='task-continuity')return taskContinuity(page,{origin:server.origin});
        if(name==='task-history')return taskHistoryBrowser(page,{origin:server.origin});
        if(name==='task-history-edges')return taskHistoryEdges(page,{origin:server.origin});
        if(name==='switching'){
          try{const state=await fixture(page,server.origin,'chat-switching.html?conversation=alpha&locale=en&contract=navigation','ALL CHECKS PASSED');
          const shell=await shellRetention(page);
          const trigger=page.getByRole('button',{name:'Assistant information',exact:true}),dialog=page.getByRole('dialog',{name:'Assistant information',exact:true}),composer=page.locator('#pot-composer');
          const draft=await composer.inputValue();await trigger.press('Enter');await dialog.waitFor();
          await composer.click();await dialog.waitFor({state:'hidden'});
          await trigger.press('Enter');await dialog.waitFor();
          await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}})));
          const translated=page.getByRole('dialog',{name:'助手信息',exact:true});await translated.waitFor();
          if(await translated.getByRole('button',{name:'助手设置',exact:true}).innerText()!=='助手设置')throw Error('Open assistant menu retained stale visible locale');
          await translated.getByRole('button',{name:'助手设置',exact:true}).click();await page.locator('.editor-form').waitFor();
          if(await page.getByLabel('名称',{exact:true}).inputValue()!=='Alpha')throw Error('The profile menu opened another assistant');
          const settings=await page.evaluate(()=>chatSwitchingIO().settings.slice(-2));if(settings.length!==2||settings.some(read=>read.space!=='one'||read.owner!=='alpha'))throw Error('Profile menu changed its captured settings target: '+JSON.stringify(settings));
          await page.locator('.titlebar').getByRole('button',{name:'返回',exact:true}).click();await composer.waitFor();
          if(await composer.inputValue()!==draft)throw Error('Selected assistant settings lost the draft');
          await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'en'}})));
          await trigger.press('Enter');await dialog.waitFor();await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'});
          await page.waitForFunction(()=>document.querySelector('.assistant-profile-trigger')?.getAttribute('aria-expanded')==='false');
          if(await trigger.getAttribute('aria-expanded')!=='false'||await composer.inputValue()!==draft)throw Error('Assistant menu dismissal lost its accessible state or draft');
          return {...state,shell,menuKeyboardAndOutsideDismissal:true,menuLocaleAndTarget:true};
          }finally{await page.evaluate(()=>window.chatSwitchingDispose?.());}
        }
        // The CLI expression derives its origin without loading the application.
        return contract('pot-ui-chat-continuity-browser.js',page,{origin:server.origin,witness:witnessMessages});
      });
      completed++;console.log('PASS '+name+' '+JSON.stringify({seconds:+((performance.now()-start)/1000).toFixed(2),result}));
    }
  }finally{await browser?.close();await server.close();console.log(JSON.stringify({group:values.group,completed,total:selected.length,seconds:+((performance.now()-started)/1000).toFixed(2),fault:values.fault}));}
}

// Representative fixture configurations, not full locale/theme/viewport products.
async function shellRetention(page){
  const history=page.locator('.history'),composer=page.locator('#pot-composer'),search=page.getByRole('searchbox',{name:'Search assistants',exact:true}),list=page.locator('.assistant-list');
  const before=await history.evaluate(node=>node.scrollTop);await history.hover();await page.mouse.wheel(0,-180);
  await page.waitForFunction(top=>document.querySelector('.history').scrollTop<top&&!document.querySelector('.latest-button').hidden,before);
  await page.evaluate(async()=>{let previous,stable=0;for(let i=0;i<90;i++){await new Promise(requestAnimationFrame);const h=document.querySelector('.history'),value=[h.scrollTop,h.scrollHeight].join(':');stable=value===previous?stable+1:0;previous=value;if(stable===3)return;}throw Error('Reading geometry did not settle before folding');});
  const capture=()=>page.evaluate(()=>{const h=document.querySelector('.history'),top=h.getBoundingClientRect().top;const row=[...h.querySelectorAll('article.message')].find(node=>{const r=node.getBoundingClientRect();return r.height&&r.bottom>top&&r.top<top+h.clientHeight;});return {id:row?.dataset.messageId,offset:row?.getBoundingClientRect().top-top};});
  const anchor=await capture();if(!anchor.id)throw Error('Fold retention has no historical message identity');
  await search.fill('a');await list.hover();await page.mouse.wheel(0,180);await page.waitForFunction(()=>document.querySelector('.assistant-list').scrollTop>0);
  await page.evaluate(anchor=>{window.shellRetention={anchor,composer:document.querySelector('#pot-composer'),search:document.querySelector('.assistant-search'),list:document.querySelector('.assistant-list'),scroll:document.querySelector('.assistant-list').scrollTop};},anchor);
  const draft=await composer.inputValue(),reads=await page.evaluate(()=>chatSwitchingIO());
  let worstDrift=0;
  const retained=async(label,{scroll=true}={})=>{
    await page.waitForFunction(saved=>{const h=document.querySelector('.history'),row=h.querySelector(`article.message[data-message-id="${saved.id}"]`);return row&&Math.abs(row.getBoundingClientRect().top-h.getBoundingClientRect().top-saved.offset)<=3;},anchor);
    const state=await page.evaluate(()=>({composer:shellRetention.composer===document.querySelector('#pot-composer'),search:shellRetention.search===document.querySelector('.assistant-search'),list:shellRetention.list===document.querySelector('.assistant-list'),query:document.querySelector('.assistant-search').value,scroll:document.querySelector('.assistant-list').scrollTop,savedScroll:shellRetention.scroll,offset:document.querySelector(`article.message[data-message-id="${shellRetention.anchor.id}"]`).getBoundingClientRect().top-document.querySelector('.history').getBoundingClientRect().top}));
    worstDrift=Math.max(worstDrift,Math.abs(state.offset-anchor.offset));
    if(!state.composer||!state.search||!state.list||state.query!=='a'||scroll&&state.scroll!==state.savedScroll||await composer.inputValue()!==draft)throw Error(label+': '+JSON.stringify({anchor,state}));
  };
  await page.getByRole('button',{name:'Collapse assistant list',exact:true}).click();await page.locator('.assistant-directory').waitFor({state:'hidden'});await retained('Historical fold',{scroll:false});
  await page.getByRole('button',{name:'Expand assistant list',exact:true}).click();await retained('Historical expand');
  if(JSON.stringify(await page.evaluate(()=>chatSwitchingIO()))!==JSON.stringify(reads))throw Error('Fold/expand fetched backend data');
  await page.getByRole('button',{name:'Collapse assistant list',exact:true}).click();
  await page.getByRole('button',{name:'Settings',exact:true}).first().click();await history.waitFor({state:'hidden'});await page.goBack();await history.waitFor();
  if(!await page.getByRole('button',{name:'Expand assistant list',exact:true}).isVisible())throw Error('Settings return lost the collapsed preference');await retained('Collapsed settings return',{scroll:false});
  await page.setViewportSize({width:390,height:800});await page.waitForFunction(()=>!document.querySelector('.directory-toggle'));await page.locator('.narrow-navigation').click();await page.locator('.assistant-directory').waitFor();
  const phone=await page.evaluate(()=>({query:document.querySelector('.assistant-search').value,scroll:document.querySelector('.assistant-list').scrollTop,saved:shellRetention.scroll}));if(phone.query!=='a'||phone.scroll!==phone.saved)throw Error('Phone directory lost retained search/scroll: '+JSON.stringify(phone));
  // The reader scrolls back to the selected row before selecting it. Preserve
  // that new directory position on the subsequent width change.
  await list.hover();await page.mouse.wheel(0,-180);await page.waitForFunction(()=>document.querySelector('.assistant-list').scrollTop===0);await page.evaluate(()=>shellRetention.scroll=0);
  await page.locator('.assistant-row[data-view-key=\'["one","alpha"]\']').click();await history.waitFor();await page.setViewportSize({width:1280,height:800});await page.waitForFunction(()=>!!document.querySelector('.directory-toggle'));
  if(!await page.getByRole('button',{name:'Expand assistant list',exact:true}).isVisible())throw Error('Phone-to-wide return lost the collapsed preference');
  await page.getByRole('button',{name:'Expand assistant list',exact:true}).click();await retained('Phone-to-wide return');
  return {historicalAnchor:anchor,worstDrift,retainedNodesAndSearch:true,foldBackendReads:0,collapsedSettingsAndPhone:true};
}

async function focused(page,name,origin){
  const phone=name.endsWith('-phone')||name==='usage-trend'||name==='linked-approvals'||name==='first-connection-codex';
  const locale=phone?'zh-CN':'en';
  await page.setViewportSize({width:phone?390:1200,height:844});
  const result=async key=>{
    await page.waitForFunction(key=>!!window[key],key);
    const state=await page.evaluate(key=>window[key],key);
    if(!state.passed)throw Error(JSON.stringify(state));return state;
  };
  if(name.startsWith('first-connection-')){
    return contract('pot-ui-first-connection-browser.js',page,{origin,state:{kind:name.slice('first-connection-'.length),width:phone?390:1200,locale}});
  }
  if(name.startsWith('assistant-start-')){
    await page.goto(origin+'/scripts/tests/fixtures/assistant-start.html?locale='+locale+'&theme=light');
    return result('assistantStartResults');
  }
  if(name.startsWith('settings-')){
    await page.goto(origin+'/scripts/tests/fixtures/assistant-settings.html?locale='+locale+'&theme=light');
    return result('assistantSettingsResults');
  }
  if(name.startsWith('composer-')){
    await page.goto(origin+'/scripts/tests/fixtures/composer-selectors.html?conversation=alpha&locale='+locale+(phone?'&mobile=1':''));
    const state=await result('composerSelectorResults');
    await contract('pot-ui-composer-mentions-browser.js',page,{origin});
    return state;
  }
  if(name.startsWith('usage-')){
    const scenario=name.slice(6);
    await page.goto(origin+'/scripts/tests/fixtures/usage-compaction.html?locale='+locale+'&theme=light&case='+scenario+(scenario==='trend'?'&conversation=1':''));
    await page.waitForFunction(()=>/^(PASS|FAIL)/.test(document.querySelector('#result')?.textContent||''));
    const state=await page.locator('#result').textContent();if(!state.startsWith('PASS'))throw Error(state);
    if(scenario==='trend')await contract('pot-ui-usage-interactions.mjs',page,{origin});
    return {passed:true};
  }
  await page.goto(origin+'/scripts/tests/fixtures/linked-approvals.html?assistant_id=main&locale=zh-CN&theme=light');
  if(name==='patch-approvals')return contract('pot-ui-patch-approvals-browser.js',page,{origin});
  await page.locator('.approval-card summary').click();
  if(await page.locator('.approval-actions button').count()!==2)throw Error('Approval broader than one operation');
  if(!(await page.locator('.approval-arguments').first().innerText()).includes('ssh deployment-host hostname'))throw Error('Missing exact command');
  await page.evaluate(()=>linkedApprovalFixture.language('en'));
  await page.getByRole('button',{name:'Deny',exact:true}).click();await page.locator('.chat-approvals').waitFor({state:'hidden'});
  if(await page.locator('.approval-actions button').count())throw Error('Stale decision remains');
  await page.evaluate(()=>linkedApprovalFixture.reset());await page.locator('.approval-card summary').click();
  await page.getByRole('button',{name:'Allow once',exact:true}).click();await page.waitForFunction(()=>!linkedApprovalFixture.pending);
  const state=await page.evaluate(()=>({calls:linkedApprovalFixture.calls,errors:linkedApprovalFixture.errors}));
  if(JSON.stringify(state.calls.map(call=>call.action))!==JSON.stringify(['deny','allow_once'])||state.errors.length)throw Error(JSON.stringify(state));
  return {passed:true};
}
