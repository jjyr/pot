import {parseArgs} from 'node:util';
import {performance} from 'node:perf_hooks';
import {groups,faults,select,fixtures} from './ui-regression-plan.mjs';
import {sourceServer,launchBrowser,isolatedPage,contract,fixture} from './browser-harness.mjs';
import {taskContinuity,witnessMessages} from './task-continuity-browser.mjs';
import {taskHistoryBrowser,taskHistoryEdges} from './task-history-browser.mjs';
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
      if(name==='directory')result=await contract('pot-ui-mobile-directory-browser.js',null,{browser,origin:server.origin});
      else if(name==='composer-enter')result=await contract('pot-ui-mobile-enter-browser.js',null,{browser,origin:server.origin,base:server.origin+'/web/src'});
      else result=await isolatedPage(browser,server.origin,async page=>{
        if(/^(assistant-start|first-connection|settings|composer|usage)-/.test(name)||name==='linked-approvals'||name==='patch-approvals')return focused(page,name,server.origin);
        if(fixtures[name])return fixture(page,server.origin,name,fixtures[name]);
        if(name==='scroll-components')return fixture(page,server.origin,'chat-scroll.html?contract=window','DONE scrolling checks');
        if(name==='task-continuity')return taskContinuity(page,{origin:server.origin});
        if(name==='task-history')return taskHistoryBrowser(page,{origin:server.origin});
        if(name==='task-history-edges')return taskHistoryEdges(page,{origin:server.origin});
        if(name==='switching'){
          return fixture(page,server.origin,'chat-switching.html?conversation=alpha&locale=en','ALL CHECKS PASSED');
        }
        // The CLI expression derives its origin without loading the application.
        return contract('pot-ui-chat-continuity-browser.js',page,{origin:server.origin,witness:witnessMessages});
      });
      completed++;console.log('PASS '+name+' '+JSON.stringify({seconds:+((performance.now()-start)/1000).toFixed(2),result}));
    }
  }finally{await browser?.close();await server.close();console.log(JSON.stringify({group:values.group,completed,total:selected.length,seconds:+((performance.now()-started)/1000).toFixed(2),fault:values.fault}));}
}

// Representative fixture configurations, not full locale/theme/viewport products.
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
