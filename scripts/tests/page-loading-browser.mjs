import assert from 'node:assert/strict';
import {sourceServer,launchBrowser} from './browser-harness.mjs';

const source=await sourceServer(),browser=await launchBrowser();
try{
 for(const locale of ['en','zh-CN']){
  const page=await browser.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
  try{
   await page.goto(source.origin+'/scripts/tests/fixtures/assistant-settings.html?locale='+locale);
   await page.waitForFunction(()=>window.assistantSettingsResults);
   const settings=await page.evaluate(()=>window.assistantSettingsResults);
   assert(settings.passed,settings.error);
   const result=await page.evaluate(async locale=>{
    const {createRecordPagination}=await import('/web/src/components/record-groups.mjs');
    const {notificationSetting}=await import('/web/src/components/notification-setting.mjs');
    const {translate}=await import('/web/src/locales/messages.mjs');
    const check=(value,message)=>{if(!value)throw Error(message);};
    const pager=createRecordPagination({locale:()=>locale,label:'Pages',move(){}});document.body.append(pager.root);
    pager.update({loading:true});check(pager.root.getAttribute('aria-busy')==='true'&&!/Loading|正在加载/.test(pager.root.textContent),'Initial pagination uses a neutral page number');
    pager.update({offset:20,total:100,count:20,nextOffset:40});const summary=pager.root.querySelector('.record-page-summary').textContent,height=pager.root.getBoundingClientRect().height;
    pager.update({offset:20,loading:true});check(pager.root.querySelector('.record-page-summary').textContent===summary&&pager.root.getBoundingClientRect().height===height,'Refreshing pagination retains summary and height');
    let notify;const notifications={state:{enabled:true,permission:'loading'},subscribe(fn){notify=fn;return()=>{};}};
    const settings=notificationSetting({notifications,locale:()=>locale,desktop:false});document.body.append(settings.element);
    check(!settings.element.textContent.includes(translate(locale,'notifications.loading')),'Notification lookup stays silent');
    notifications.state={enabled:true,permission:'denied'};notify();check(settings.element.textContent.includes(translate(locale,'notifications.deniedBrowser')),'Permission failures remain visible');settings.dispose();settings.element.remove();pager.root.remove();
    return {paginationStable:true,notificationLoadingSilent:true};
   },locale);
   assert.deepEqual(errors,[]);console.log(JSON.stringify({locale,settingsChecks:settings.checks.length,...result}));
  }finally{await page.close();}
 }
}finally{await browser.close();await source.close();}
