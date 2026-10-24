// Real production Chat and Task detail routes, using only the in-memory fixture.
import assert from 'node:assert/strict';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const {chromium}=await import(process.env.POT_PLAYWRIGHT_MODULE?pathToFileURL(resolve(process.env.POT_PLAYWRIGHT_MODULE)).href:'playwright');
const origin=process.argv[2]||'http://127.0.0.1:17723';
const browser=await chromium.launch({headless:true,...(process.env.POT_CHROMIUM_EXECUTABLE?{executablePath:process.env.POT_CHROMIUM_EXECUTABLE}:{})});
try{
  for(const [width,theme,locale]of [[1280,'light','en'],[1280,'dark','zh-CN'],[390,'light','zh-CN'],[390,'dark','en'],[320,'light','en'],[320,'system','zh-CN']]){
    const page=await browser.newPage({viewport:{width,height:1100},colorScheme:theme==='system'?'dark':theme,reducedMotion:'reduce'}),errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    await page.goto(`${origin}/scripts/tests/fixtures/task-communication.html?identity=1&scenario=collapsed&locale=${locale}&theme=${theme}`);
    if(width<720)await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
    await page.evaluate(()=>window.taskCommunication.prepare());
    await page.waitForSelector('.background-task-identity',{state:'attached'});
    const collapsed=await page.evaluate(async()=>{
      const {taskIdentities,taskColorIndex}=await import('/web/src/state/task-identity.mjs');
      const f=window.taskCommunication,summary=[...document.querySelectorAll('.background-task-identity')],rows=[...document.querySelectorAll('.background-task')];
      const marks=node=>[node.querySelector('.task-identity-mark').dataset.taskColor,node.querySelector('.task-identity-short').textContent];
      const identities=f.catalogue.slice(0,3).map((task,index)=>{
        const label=taskIdentities.short(task),communications=[...document.querySelectorAll('.communication-task-endpoint')].filter(node=>node.querySelector('.task-identity-short').textContent===label);
        return communications.length>0&&[rows[index],summary[index],...communications].every(node=>JSON.stringify(marks(node))===JSON.stringify([String(taskColorIndex(task)),label]));
      });
      const visible=summary.filter(node=>!node.hidden),more=document.querySelector('.background-task-more');
      return {identities,paletteCollision:new Set(f.catalogue.slice(0,3).map(taskColorIndex)).size<3,shortUnique:new Set(f.catalogue.slice(0,3).map(task=>taskIdentities.short(task))).size===3,summaryCount:visible.length+Number(more.textContent.replace('+','')),summaryPrefix:visible.every((node,index)=>node===summary[index]),unknown:marks(summary[3]),size:document.querySelector('.task-identity-mark').getBoundingClientRect().width,overflow:document.documentElement.scrollWidth>innerWidth};
    });
    assert.deepEqual(collapsed.identities,[true,true,true]);assert.equal(collapsed.summaryCount,7);assert.equal(collapsed.summaryPrefix,true);
    assert.equal(collapsed.paletteCollision,true);assert.equal(collapsed.shortUnique,true);
    assert.equal(collapsed.unknown[0],undefined);assert.equal(collapsed.size,22);assert.equal(collapsed.overflow,false);
    await page.locator('.communication-stack-summary').focus();await page.keyboard.press('Enter');
    await page.waitForSelector('.communication-entry');
    assert.equal(await page.locator('.communication-entry').count(),7);
    assert.equal(await page.evaluate(()=>[...document.querySelectorAll('.communication-entry .communication-task-endpoint')].every(node=>node.querySelector('.task-identity-mark[data-task-color]')&&node.querySelector('.task-identity-short').textContent)),true);
    // The same linked Task must route through its local catalogue key in all four places.
    for(const selector of ['.communication-stack-routes .communication-task-endpoint','.communication-entry .communication-task-endpoint','.background-task','.background-task-identity']){
      const target=page.locator(selector).filter({hasText:selector.includes('background-task-identity')?'#8a1c1@review-r':'Remote review'}).first();
      if(selector.includes('background-task-identity')&&!await target.isVisible()){
        // Wide layout exposes all first three identities; a narrow prefix may hide the remote one.
        if(width<720)continue;
      }
      await target.focus();await page.keyboard.press('Enter');
      await page.waitForSelector('.task-conversation');
      assert.equal(await page.evaluate(()=>history.state.potTask.id),'local-hash');
      assert.equal(await page.evaluate(()=>{const r=window.taskCommunication.requests.filter(item=>item.path==='/api/v1/tasks/local-hash').at(-1);return r?.space==='review-local'&&r?.owner==='pot';}),true);
      await page.goBack();await page.waitForSelector('.background-task');
      assert.equal(await page.locator('.communication-stack').getAttribute('open'),'','detail Back retains stack expansion');
    }
    await page.locator('.background-tasks-toggle').focus();await page.keyboard.press('Enter');
    await page.waitForFunction(()=>document.querySelectorAll('.background-task').length===7);
    assert.equal(await page.locator('.background-task-identities').isHidden(),true);
    const stable=await page.evaluate(async()=>{
      const f=window.taskCommunication,row=document.querySelector('.background-task'),mark=row.querySelector('.task-identity-mark'),short=row.querySelector('.task-identity-short'),color=mark.dataset.taskColor,label=short.textContent;
      row.focus();const order=[...document.querySelectorAll('.background-task')].map(node=>node.dataset.viewKey);
      // This represents catalogue revalidation and a new short-label collision, without transcript writes.
      await f.tasks(tasks=>{tasks[0].phase='queued';tasks[0].title+=' renamed';tasks.push({id:'new-collision',address:{...f.taskB,agent_id:'8a1cf100-new'},title:'New collision',phase:'running'});});
      await new Promise(resolve=>setTimeout(resolve,100));
      const after=document.querySelector('.background-task');
      return {retained:row===after&&mark===after.querySelector('.task-identity-mark')&&short===after.querySelector('.task-identity-short'),focus:document.activeElement===row,color:mark.dataset.taskColor===color,label:short.textContent===label,phase:after.querySelector('.task-activity-dot').dataset.phase,order:JSON.stringify(order)===JSON.stringify([...document.querySelectorAll('.background-task')].slice(0,7).map(node=>node.dataset.viewKey)),unknown:document.querySelectorAll('.background-task')[3].querySelector('.task-identity-mark').hasAttribute('data-task-color'),overflow:document.documentElement.scrollWidth>innerWidth};
    });
    assert.deepEqual(stable,{retained:true,focus:true,color:true,label:true,phase:'queued',order:true,unknown:false,overflow:false});
    // New collision lengthens the same label in communication, expanded rows and folded summary.
    await page.locator('.background-tasks-toggle').click();
    const collision=await page.evaluate(()=>{
      const nodes=[...document.querySelectorAll('.task-identity-short')].filter(node=>node.textContent.startsWith('#8a1cf'));
      return nodes.length>3&&nodes.every(node=>['#8a1cf0','#8a1cf1'].includes(node.textContent));
    });assert.equal(collision,true);
    const more=page.locator('.background-task-more');
    if(await more.isVisible()){await more.focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelectorAll('.background-task').length===8);}
    await page.evaluate(()=>window.taskCommunication.update(messages=>{const f=window.taskCommunication;messages.splice(-1,0,{...f.send('unmapped',{node_id:'unknown-node',kind:'task',agent_id:'unmapped-identity'},'Unknown full address'),sequence:'100',version:1});}));
    const unknown=await page.evaluate(()=>{
      const endpoint=[...document.querySelectorAll('.communication-task-endpoint')].find(node=>node.querySelector('.task-identity-short').textContent==='#unma');
      return !!endpoint&&endpoint.tagName==='SPAN'&&!endpoint.querySelector('.task-identity-mark').hasAttribute('data-task-color');
    });assert.equal(unknown,true,'unmapped complete addresses retain neutral labels and have no guessed detail route');
    await page.evaluate(async()=>{
      const {taskIdentities}=await import('/web/src/state/task-identity.mjs');
      const f=window.taskCommunication,foreign={id:'local-hash',address:{...f.taskC,node_id:'review-foreign'},title:'Foreign mapped task',_space_id:'foreign-scope',assistant_id:'foreign-owner',phase:'running'};
      taskIdentities.register([foreign]);const request=f.client.request;
      f.client.request=async(path,options={})=>{
        if(path==='/api/v1/tasks/local-hash'&&options.space==='foreign-scope'){f.requests.push({path,space:options.space,owner:options.owner});return {info:foreign,request:{text:foreign.title},capabilities:{version:1,operations:['task.read']}};}
        return request(path,options);
      };
      await f.update(messages=>messages.splice(-1,0,{...f.send('foreign',foreign.address,'Another confirmed catalogue scope'),sequence:'101',version:1}));
    });
    await page.locator('.communication-stack-routes .communication-task-endpoint').filter({hasText:'Foreign mapped task'}).click();await page.waitForSelector('.task-conversation');
    assert.equal(await page.evaluate(()=>history.state.potTask._space_id==='foreign-scope'&&history.state.potTask.assistant_id==='foreign-owner'),true,'equal catalogue IDs never overwrite a confirmed address scope');
    assert.equal(await page.evaluate(()=>window.taskCommunication.requests.some(item=>item.path==='/api/v1/tasks/local-hash'&&item.space==='foreign-scope'&&item.owner==='foreign-owner')),true);
    await page.goBack();await page.waitForSelector('.background-task');
    const status=await page.evaluate(async()=>{
      const {createBackgroundTasks}=await import('/web/src/components/background-tasks.mjs');
      const f=window.taskCommunication,view=createBackgroundTasks({locale:()=>document.documentElement.lang,onOpen:()=>{throw Error('No navigation in status probe');},onToggle:()=>{}});
      const marks=[];let labels=true;
      for(const phase of ['running','queued','failed','complete']){
        view.update('status',[{...f.catalogue[0],phase}],()=>false);
        marks.push([view.element.querySelector('.task-identity-mark').dataset.taskColor,view.element.querySelector('.background-task-label .task-identity-short').textContent]);
        labels&&=!view.element.querySelector('.background-task-status').textContent.startsWith('tasks.');
      }
      view.close();return labels&&marks.every(mark=>JSON.stringify(mark)===JSON.stringify(marks[0]));
    });assert.equal(status,true,'running/queued/failed/complete never recolor the task identity');
    assert.deepEqual(errors,[]);
    console.log(`PASS identity ${width}px ${theme} ${locale}: matching marks/IDs, mapped detail routes, +N, keyboard, phase/rename/collision, keyed DOM`);
    await page.close();
  }
}finally{await browser.close();}
