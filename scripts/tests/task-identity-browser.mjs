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
    await page.waitForSelector('.background-task',{state:'attached'});
    const collapsed=await page.evaluate(async()=>{
      const {icon}=await import('/web/src/components/icons.mjs');
      const {taskIdentities,taskColorIndex}=await import('/web/src/state/task-identity.mjs');
      const f=window.taskCommunication,rows=[...document.querySelectorAll('.background-task')];
      const marks=node=>[node.querySelector('.task-identity-mark').dataset.taskColor,node.querySelector('.task-identity-short').textContent];
      const identities=f.catalogue.slice(0,3).map((task,index)=>{
        const label=taskIdentities.short(task),communications=[...document.querySelectorAll('.work-participant')].filter(node=>node.querySelector('.task-identity-short').textContent===label);
        return communications.length>0&&[rows[index],...communications].every(node=>JSON.stringify(marks(node))===JSON.stringify([String(taskColorIndex(task)),label]));
      });
      const singleLine=rows.every(row=>{
        const children=[...row.children].filter(child=>!child.classList.contains('visually-hidden')),boxes=children.map(child=>child.getBoundingClientRect());
        return row.getBoundingClientRect().height>=44&&boxes.every(box=>Math.abs((box.top+box.height/2)-(boxes[0].top+boxes[0].height/2))<2)&&row.scrollWidth<=row.clientWidth;
      });
      return {identities,labels:f.catalogue.slice(0,3).map(task=>taskIdentities.short(task)),singleLine,summaryCount:document.querySelectorAll('.background-task-identities,.background-task-identity,.background-task-more').length,size:document.querySelector('.task-identity-mark').getBoundingClientRect().width,overflow:document.documentElement.scrollWidth>innerWidth,
        icon:[...document.querySelectorAll('.task-identity-mark')].every(mark=>mark.firstElementChild.outerHTML===icon('tasks').outerHTML)};
    });
    assert.deepEqual(collapsed.identities,[true,true,true]);assert.deepEqual(collapsed.labels,['#a078','#b169','#c25a']);
    assert.equal(collapsed.singleLine,true);assert.equal(collapsed.summaryCount,0);assert.equal(collapsed.icon,true);
    assert.equal(collapsed.size,22);assert.equal(collapsed.overflow,false);
    await page.locator('.work-record-summary').focus();await page.keyboard.press('Enter');
    await page.locator('.communication-tool').first().waitFor();
    await page.evaluate(()=>{for(const d of document.querySelectorAll('.communication-tool'))if(!d.open)d.querySelector('summary').click();});
    await page.waitForSelector('.communication-entry');
    assert.equal(await page.locator('.communication-entry').count(),7);
    assert.equal(await page.evaluate(()=>[...document.querySelectorAll('.communication-entry .communication-task-endpoint')].every(node=>node.querySelector('.task-identity-mark[data-task-color]')&&node.querySelector('.task-identity-short').textContent)),true);
    // The same mapped Task must route through its catalogue key in all three places.
    for(const selector of ['.communication-entry .communication-task-endpoint','.background-task']){
      const target=page.locator(selector).filter({hasText:'Remote review'}).first();
      await target.focus();await page.keyboard.press('Enter');
      await page.waitForSelector('.task-conversation');
      assert.equal(await page.evaluate(()=>history.state.potTask.id),await page.evaluate(()=>window.taskCommunication.catalogue[2].id));
      assert.equal(await page.evaluate(()=>{const r=window.taskCommunication.requests.filter(item=>item.path==='/api/v1/tasks/'+window.taskCommunication.catalogue[2].id).at(-1);return r?.space==='review-local'&&r?.owner==='pot';}),true);
      await page.goBack();await page.waitForSelector('.background-task');
      assert.equal(await page.locator('.work-records').getAttribute('open'),'','detail Back retains stack expansion');
    }
    await page.locator('.background-tasks-toggle').focus();await page.keyboard.press('Enter');
    await page.waitForFunction(()=>document.querySelectorAll('.background-task').length===7);
    assert.equal(await page.locator('.background-task-identities').count(),0);
    const stable=await page.evaluate(async()=>{
      const f=window.taskCommunication,row=document.querySelector('.background-task'),mark=row.querySelector('.task-identity-mark'),short=row.querySelector('.task-identity-short'),color=mark.dataset.taskColor,label=short.textContent;
      row.focus();const order=[...document.querySelectorAll('.background-task')].map(node=>node.dataset.viewKey);
      // This represents catalogue revalidation and a new short-label collision, without transcript writes.
      await f.tasks(tasks=>{tasks[0].phase='queued';tasks[0].title+=' renamed';tasks.push({id:'new-collision',address:{...f.taskB,agent_id:'b16991fb-d701-4f39-aed0-ae2edec1245b'},title:'New collision',phase:'running'});});
      await new Promise(resolve=>setTimeout(resolve,100));
      const after=document.querySelector('.background-task');
      return {retained:row===after&&mark===after.querySelector('.task-identity-mark')&&short===after.querySelector('.task-identity-short'),focus:document.activeElement===row,color:mark.dataset.taskColor===color,label:short.textContent===label,phase:after.querySelector('.task-activity-dot').dataset.phase,order:JSON.stringify(order)===JSON.stringify([...document.querySelectorAll('.background-task')].slice(0,7).map(node=>node.dataset.viewKey)),unknown:document.querySelectorAll('.background-task')[3].querySelector('.task-identity-mark').hasAttribute('data-task-color'),overflow:document.documentElement.scrollWidth>innerWidth};
    });
    assert.deepEqual(stable,{retained:true,focus:true,color:true,label:true,phase:'queued',order:true,unknown:false,overflow:false});
    // Even confirmed distinct full IDs with equal prefixes keep four-character labels.
    await page.locator('.background-tasks-toggle').click();
    const collision=await page.evaluate(()=>{
      const nodes=[...document.querySelectorAll('.task-identity-short')].filter(node=>node.textContent.startsWith('#b169'));
      return nodes.length>3&&nodes.every(node=>node.textContent==='#b169');
    });assert.equal(collision,true);
    await page.evaluate(()=>window.taskCommunication.update(messages=>{const f=window.taskCommunication;messages.splice(-1,0,{...f.send('unmapped',{node_id:'unknown-node',kind:'task',agent_id:'unmapped-identity'},'Unknown full address'),sequence:'100',version:1});}));
    await page.evaluate(()=>{for(const d of document.querySelectorAll('.work-records'))if(!d.open)d.querySelector('summary').click();});
    await page.locator('[data-call-id="unmapped"]>summary').click();
    const unknown=await page.evaluate(()=>{
      const endpoint=[...document.querySelectorAll('.communication-task-endpoint')].find(node=>node.querySelector('.task-identity-short').textContent==='#unma');
      return !!endpoint&&endpoint.tagName==='SPAN'&&!endpoint.querySelector('.task-identity-mark').hasAttribute('data-task-color');
    });assert.equal(unknown,true,'unmapped complete addresses retain neutral labels and have no guessed detail route');
    await page.evaluate(async()=>{
      const {taskIdentities}=await import('/web/src/state/task-identity.mjs');
      const f=window.taskCommunication,foreign={id:f.catalogue[2].id,address:{...f.taskC,node_id:'review-foreign'},title:'Foreign mapped task',_space_id:'foreign-scope',assistant_id:'foreign-owner',phase:'running'};
      taskIdentities.register([foreign]);const request=f.client.request;
      f.client.request=async(path,options={})=>{
        if(path==='/api/v1/tasks/'+f.catalogue[2].id&&options.space==='foreign-scope'){f.requests.push({path,space:options.space,owner:options.owner});return {info:foreign,request:{text:foreign.title},capabilities:{version:1,operations:['task.read']}};}
        return request(path,options);
      };
      await f.update(messages=>messages.splice(-1,0,{...f.send('foreign',foreign.address,'Another confirmed catalogue scope'),sequence:'101',version:1}));
    });
    await page.evaluate(()=>{for(const d of document.querySelectorAll('.work-records'))if(!d.open)d.querySelector('summary').click();});
    await page.locator('[data-call-id="foreign"]>summary').click();
    await page.locator('[data-call-id="foreign"] .communication-entry .communication-task-endpoint').filter({hasText:'Foreign mapped task'}).click();await page.waitForSelector('.task-conversation');
    assert.equal(await page.evaluate(()=>history.state.potTask._space_id==='foreign-scope'&&history.state.potTask.assistant_id==='foreign-owner'),true,'equal catalogue IDs never overwrite a confirmed address scope');
    assert.equal(await page.evaluate(()=>window.taskCommunication.requests.some(item=>item.path==='/api/v1/tasks/'+window.taskCommunication.catalogue[2].id&&item.space==='foreign-scope'&&item.owner==='foreign-owner')),true);
    await page.goBack();await page.waitForSelector('.background-task');
    const status=await page.evaluate(async()=>{
      const {createBackgroundTasks}=await import('/web/src/components/background-tasks.mjs');
      const f=window.taskCommunication,view=createBackgroundTasks({locale:()=>document.documentElement.lang,onOpen:()=>{throw Error('No navigation in status probe');},onToggle:()=>{}});
      const marks=[];let labels=true;
      for(const phase of ['running','queued','failed','complete']){
        view.update('status',[{...f.catalogue[0],phase}],()=>false);
        marks.push([view.element.querySelector('.task-identity-mark').dataset.taskColor,view.element.querySelector('.task-identity-short').textContent]);
        labels&&=!view.element.querySelector('.background-task-status').textContent.startsWith('tasks.');
      }
      view.close();return labels&&marks.every(mark=>JSON.stringify(mark)===JSON.stringify(marks[0]));
    });assert.equal(status,true,'running/queued/failed/complete never recolor the task identity');
    assert.deepEqual(errors,[]);
    console.log(`PASS identity ${width}px ${theme} ${locale}: original icon, four-character IDs, single-line rows, no duplicate summary, mapped detail routes, keyboard, phase/rename/collision, keyed DOM`);
    await page.close();
  }
  // Repeated prefixes and equal full IDs across nodes are deliberate boundary data,
  // separate from the default UUID-shaped screenshot fixture.
  for(const width of [1280,320]){
    const page=await browser.newPage({viewport:{width,height:1100},reducedMotion:'reduce'});
    await page.goto(`${origin}/scripts/tests/fixtures/task-communication.html?identity=1&edge=1&scenario=collapsed&locale=en&theme=light`);
    if(width<720)await page.locator('.assistant-row').filter({hasText:'Pot'}).click();
    await page.evaluate(()=>window.taskCommunication.prepare());await page.waitForSelector('.background-task');
    assert.deepEqual(await page.locator('.background-task .task-identity-short').allTextContents(),['#8a1c','#8a1c','#8a1c']);
    for(const [title,index]of [['Layout review',0],['Responsive checks',1],['Remote review',2]]){
      await page.locator('.background-task').filter({hasText:title}).click();await page.waitForSelector('.task-conversation');
      assert.equal(await page.evaluate(()=>history.state.potTask.id),await page.evaluate(index=>window.taskCommunication.catalogue[index].id,index));
      await page.goBack();await page.waitForSelector('.background-task');
    }
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    assert.deepEqual(await page.locator('.background-task .task-identity-short').allTextContents(),['#8a1c','#8a1c','#8a1c']);
    console.log(`PASS identity edge ${width}px: repeated prefixes stay four characters; cross-node equal IDs keep distinct detail routes`);
    await page.close();
  }
}finally{await browser.close();}
