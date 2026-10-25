import {parseArgs} from 'node:util';
import {performance} from 'node:perf_hooks';
import {groups,faults,select,fixtures} from './ui-regression-plan.mjs';
import {sourceServer,launchBrowser,isolatedPage,contract,fixture} from './browser-harness.mjs';
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
      else result=await isolatedPage(browser,server.origin,async page=>{
        if(fixtures[name])return fixture(page,server.origin,name,fixtures[name]);
        if(name==='scroll-components')return fixture(page,server.origin,'chat-scroll.html?conversation=alpha&locale=en','DONE scrolling checks');
        if(name==='task-history')return taskHistoryBrowser(page,{origin:server.origin});
        if(name==='task-history-edges')return taskHistoryEdges(page,{origin:server.origin});
        if(name==='switching'){
          return fixture(page,server.origin,'chat-switching.html?conversation=alpha&locale=en','ALL CHECKS PASSED');
        }
        // The CLI expression derives its origin without loading the application.
        await page.goto(server.origin+'/scripts/tests/fixtures/task-history-edges.html');
        return contract('pot-ui-chat-continuity-browser.js',page,{origin:server.origin});
      });
      completed++;console.log('PASS '+name+' '+JSON.stringify({seconds:+((performance.now()-start)/1000).toFixed(2),result}));
    }
  }finally{await browser?.close();await server.close();console.log(JSON.stringify({group:values.group,completed,total:selected.length,seconds:+((performance.now()-started)/1000).toFixed(2),fault:values.fault}));}
}
