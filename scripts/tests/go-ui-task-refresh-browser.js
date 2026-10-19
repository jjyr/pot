// Exercise the production page component with controlled, delayed API replies.
async page=>{
 const context=await page.context().browser().newContext({serviceWorkers:'block'});page=await context.newPage();
 const state=STATE,assert=(value,message)=>{if(!value)throw Error(message);},errors=[];
 const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 await page.route('**/task-refresh-fixture',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html lang="zh-CN"><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"><body><main style="height:680px;width:900px;max-width:100%;margin:40px auto;overflow:auto"></main></body></html>'}));
 try{
  await page.setViewportSize({width:1200,height:900});await page.goto(state.origin+'/task-refresh-fixture');
  await page.evaluate(async()=>{
   const {createPages}=await import('/components/pages.mjs');
   const f=window.taskFixture={calls:[],pending:[],notices:[]};
   f.items=Array.from({length:12},(_,index)=>({id:String(index),title:'分析任务 '+(index+1),phase:'running',assistant_id:'a',updated_at_ms:100-index}));
   f.client={spaces:[{id:'local',name:'本机'}],assistants:[{id:'a',space_id:'local',name:'项目助手'}],runtime:{boot_id:'fixture'},scope:()=>({space:'local',owner:'a'}),request:(path,options={})=>{
    if(path.includes('/permissions'))return Promise.resolve({items:[]});
    if(/^\/api\/v1\/tasks\/[^/]+$/.test(path)&&f.detailRecord)return Promise.resolve(structuredClone(f.detailRecord));
    if(path.endsWith('/messages'))return Promise.resolve({items:[]});
    if(path.includes('/events?'))return Promise.resolve({events:[],cursor:0});
    return new Promise((resolve,reject)=>{const call={path,options,resolve,reject};f.calls.push(call);f.pending.push(call);});
   }};
   f.reply=()=>{for(const call of f.pending.splice(0))call.resolve({items:structuredClone(f.items),counts:{active:f.items.length}});};
   f.pages=createPages({chatApprovalHost:document.createElement('div'),chatVisible:()=>false,client:f.client,host:document.querySelector('main'),approvalHost:document.createElement('div'),locale:()=> 'zh-CN',notify:message=>f.notices.push(message),changed:()=>{},titleChanged:()=>{},openChat:()=>{}});
   f.initial=f.pages.show('tasks');
  });
  await page.evaluate(async()=>{taskFixture.reply();await taskFixture.initial;});
  await page.locator('.task-row').first().waitFor();
  await page.evaluate(()=>{
   const f=taskFixture;f.input=document.querySelector('input');f.rows=document.querySelector('.task-list');f.row=f.rows.querySelector('.task-row');f.input.focus();f.input.value='未提交的搜索';f.input.setSelectionRange(2,4);f.rows.scrollTop=160;f.scroll=f.rows.scrollTop;
   f.pages.refresh(['usage']);f.pages.refresh(['permissions']);f.pages.refresh(['schedules']);
  });
  assert(await page.evaluate(()=>taskFixture.calls.length===1),'Unrelated changes do not reload task lists');
  await page.evaluate(()=>{const f=taskFixture;f.refresh=f.pages.refresh(['tasks']);for(let i=0;i<20;i++)f.pages.refresh(['tasks']);});
  assert(await page.evaluate(()=>{const f=taskFixture;return f.calls.length===2&&f.pending.length===1&&!f.pending[0].options.signal.aborted&&f.input.isConnected&&f.rows.isConnected&&f.rows.children.length>0;}),'A burst retains the visible list and one active request');
  if(state.screenshot)await page.screenshot({path:state.screenshot});
  await page.evaluate(async()=>{const f=taskFixture;f.items[0].phase='waiting';f.items[0].pending=2;f.reply();await f.refresh;});
  await page.waitForFunction(()=>taskFixture.calls.length===3);
  await page.evaluate(()=>taskFixture.reply());
  await page.waitForFunction(()=>document.querySelector('.task-phase').textContent==='等待中');
  assert(await page.evaluate(()=>{const f=taskFixture;return f.calls.length===3&&f.rows.querySelector('.task-row')===f.row&&f.rows.scrollTop===f.scroll&&document.activeElement===f.input&&f.input.value==='未提交的搜索'&&f.input.selectionStart===2&&f.input.selectionEnd===4&&f.row.textContent.includes('2 条待处理指令');}),'One follow-up updates metadata while preserving nodes, draft, selection and scroll');
  // Cached offline rows are retained and recover without repeated warnings.
  for(let attempt=0;attempt<2;attempt++){
   await page.evaluate(async()=>{const f=taskFixture;f.refresh=f.pages.refresh(['tasks']);for(const call of f.pending.splice(0))call.reject(Error('Offline fixture'));await f.refresh;});
  }
  assert(await page.evaluate(()=>taskFixture.row.isConnected&&taskFixture.row.disabled&&document.querySelectorAll('.space-availability').length===1),'Failed refreshes retain cached rows and one stale warning');
  await page.evaluate(async()=>{const f=taskFixture;f.refresh=f.pages.refresh(['tasks']);f.reply();await f.refresh;});
  assert(await page.evaluate(()=>!taskFixture.row.disabled&&!document.querySelector('.space-availability')),'Recovery clears stale state');
  await page.evaluate(async()=>{const f=taskFixture;f.focused=f.rows.querySelectorAll('.task-row')[11];f.focused.focus();f.items[11].updated_at_ms=1000;f.refresh=f.pages.refresh(['tasks']);f.reply();await f.refresh;});
  assert(await page.evaluate(()=>taskFixture.rows.querySelector('.task-row')===taskFixture.focused&&document.activeElement===taskFixture.focused),'Reordering retains row identity and keyboard focus');
  // Pagination and removals still update controls and row membership.
  await page.evaluate(async()=>{const f=taskFixture;f.items.push(...Array.from({length:10},(_,index)=>({id:'extra-'+index,title:'新增任务 '+index,assistant_id:'a',phase:'running',updated_at_ms:50-index})));f.refresh=f.pages.refresh(['tasks']);f.reply();await f.refresh;});
  await page.getByRole('button',{name:'下一页',exact:true}).click();
  await page.evaluate(()=>taskFixture.reply());
  await page.waitForFunction(()=>document.querySelectorAll('.task-row').length===2);
  assert(await page.getByRole('button',{name:'下一页',exact:true}).isDisabled()&&await page.getByRole('button',{name:'上一页',exact:true}).isEnabled(),'Pagination uses the latest total and offset');
  await page.getByRole('button',{name:'上一页',exact:true}).click();await page.evaluate(()=>taskFixture.reply());
  await page.waitForFunction(()=>document.querySelectorAll('.task-row').length===20);
  await page.evaluate(async()=>{const f=taskFixture;f.items=[];f.refresh=f.pages.refresh(['tasks']);f.reply();await f.refresh;f.input=document.querySelector('input');});
  assert(await page.locator('.task-row').count()===0&&await page.locator('.page-empty').count()===1,'Removed tasks reconcile to the empty state');
  // Older requests must not overwrite a new filter, even if cancellation is ignored.
  await page.evaluate(()=>{const f=taskFixture;f.pages.refresh(['tasks']);f.retired=f.pending.splice(0);f.input.value='新查询';f.input.dispatchEvent(new Event('change'));});
  assert(await page.evaluate(()=>taskFixture.retired[0].options.signal.aborted&&taskFixture.pending[0].path.includes('q=%E6%96%B0%E6%9F%A5%E8%AF%A2')),'Filter navigation aborts the previous generation');
  await page.evaluate(()=>{taskFixture.items=[{id:'current',assistant_id:'a',title:'当前筛选任务',phase:'running',updated_at_ms:200}];taskFixture.reply();});
  await page.getByRole('button',{name:'当前筛选任务',exact:true}).waitFor();
  await page.evaluate(()=>{for(const call of taskFixture.retired)call.resolve({items:[{id:'stale',title:'过期任务'}]});});
  assert(await page.getByRole('button',{name:'当前筛选任务',exact:true}).count()===1&&await page.getByRole('button',{name:'过期任务',exact:true}).count()===0,'Late responses cannot repaint the new view');
  // The same task ID on another assistant remains a distinct row and owner.
  await page.evaluate(async()=>{const f=taskFixture;f.client.assistants.push({id:'b',space_id:'local',name:'第二助手'});f.refresh=f.pages.refresh(['assistants']);for(const call of f.pending.splice(0))call.resolve({items:['a','b'].map(owner=>({...f.items[0],assistant_id:owner,title:'助手 '+owner})),total:2,counts:{active:2}});await f.refresh;});
  assert(await page.locator('.task-row').count()===2,'Row identity includes assistant identity');
  await page.getByRole('button',{name:'助手 b',exact:true}).click();
  await page.evaluate(()=>{taskFixture.pages.refresh(['tasks']);});
  assert(await page.evaluate(()=>taskFixture.pending.length===1&&taskFixture.pending[0].options.owner==='b'&&!taskFixture.pending[0].options.signal.aborted),'Changes during detail loading do not navigate back to the list');
  await page.evaluate(()=>{taskFixture.detailRecord={info:{phase:'running'},request:{text:'目标'}};taskFixture.pending.shift().resolve(taskFixture.detailRecord);});
  await page.getByLabel('任务追加',{exact:true}).waitFor();
  await page.getByLabel('任务追加',{exact:true}).fill('保留任务指导');
  await page.evaluate(async()=>{taskFixture.guidance=document.querySelector('textarea');taskFixture.detailRecord.info.phase='complete';taskFixture.detailRecord.result={text:'完成结果'};await taskFixture.pages.refresh(['tasks']);});
  assert(await page.getByText('完成结果',{exact:true}).count()===1,'Task metadata and results refresh with the transcript');
  assert(await page.getByLabel('任务追加',{exact:true}).getAttribute('readonly')===null,'Completed Tasks accept follow-up');
  assert(await page.evaluate(()=>taskFixture.guidance===document.querySelector('textarea')&&taskFixture.guidance.value==='保留任务指导'),'Transcript updates retain the guidance draft');
  assert(errors.length===0,errors.join('\n'));
  return {passed:true,coalesced_refresh:true,stable_dom:true,retained_search:true,offline_recovery:true,stale_responses_ignored:true,owner_identity:true,detail_navigation:true,errors};
 }finally{await page.evaluate(()=>window.taskFixture?.pages.dispose());page.off('pageerror',onError);await context.close();}
}
