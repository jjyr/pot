import assert from 'node:assert/strict';

export async function taskHistoryBrowser(page,{origin,before=false,screenshot}={}){
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(origin+'/scripts/tests/fixtures/db-driven-task.html'+(before?'?before=1':''));
  await page.waitForFunction(()=>document.querySelector('#results')?.textContent.startsWith('Ready'),null,{timeout:120000});
  await page.waitForFunction(()=>document.querySelector('.task-transcript')?.textContent.includes('Execution update 1200.'));
  const history=page.locator('.task-history');
  async function top(){await history.hover();await page.mouse.wheel(0,-1000000);}
  if(before){
    await top();await page.waitForFunction(()=>document.querySelector('.task-history')?.scrollTop===0);
    assert.equal(await page.getByRole('button',{name:'Load earlier output',exact:true}).count(),1,'Previous PR head requires the history button');
    assert.ok((await page.locator('.task-transcript').textContent()).includes('Execution update 0561.'));
    if(screenshot)await page.screenshot({path:screenshot});return;
  }
  assert.equal(await page.getByRole('button',{name:'Load earlier output',exact:true}).count(),0);
  await page.evaluate(()=>{
    const f=taskFixture;f.pagesDomainPromise=import('/web/src/state/domains/index.mjs');
    f.counts={rows:0,scans:0};f.earlier=[];f.inflight=0;f.maximum=0;
    for(const prototype of [IDBObjectStore.prototype,IDBIndex.prototype])for(const method of ['get','getAll','openCursor']){
      const call=prototype[method];prototype[method]=function(...args){const result=call.apply(this,args);if((this.objectStore?.name||this.name)==='messages'){if(method!=='get')f.counts.scans++;result.addEventListener('success',()=>f.counts.rows+=method==='getAll'?result.result.length:result.result?1:0);}return result;};
    }
  });
  await page.evaluate(async()=>{
    const f=taskFixture,{domainsFor}=await f.pagesDomainPromise,domain=domainsFor(f.client).tasks,earlier=domain.loadEarlier;
    domain.loadEarlier=async(...args)=>{
      const history=document.querySelector('.task-history'),row=[...history.querySelectorAll('article.message')].find(node=>node.getBoundingClientRect().bottom>history.getBoundingClientRect().top);
      const anchor=row?{id:row.dataset.messageId,y:row.getBoundingClientRect().top-history.getBoundingClientRect().top}:null;
      const call={before:args[2],anchor};f.earlier.push(call);f.maximum=Math.max(f.maximum,++f.inflight);
      try{const result=await earlier(...args);call.first=result?.messages[0]?.text;return result;}finally{f.inflight--;}
    };
  });
  await top();
  await page.waitForFunction(()=>taskFixture.earlier.length===1&&taskFixture.inflight===0&&taskFixture.earlier[0].first?.includes('Execution update 0481.'));
  await page.waitForFunction(()=>document.querySelector('.task-history').scrollTop>100);
  const first=await page.evaluate(()=>{
    const f=taskFixture,h=document.querySelector('.task-history'),a=f.earlier[0].anchor,row=h.querySelector(`[data-message-id="${a.id}"]`);return {counts:f.counts,maximum:f.maximum,calls:f.earlier.length,shift:row?row.getBoundingClientRect().top-h.getBoundingClientRect().top-a.y:null};
  });
  assert.deepEqual(first.counts,{rows:80,scans:1},'Automatic local pagination reads one indexed 80-row page');
  assert.equal(first.maximum,1);assert.equal(first.calls,1);assert.ok(first.shift!==null&&Math.abs(first.shift)<=3,'Prepending preserves the visible message offset: '+JSON.stringify(first));
  if(screenshot)await page.screenshot({path:screenshot});
  await top();await page.waitForFunction(()=>taskFixture.earlier.length===2&&taskFixture.inflight===0&&taskFixture.earlier[1].first?.includes('Execution update 0401.'));
  await page.evaluate(async()=>{
    const f=taskFixture;for(let i=0;i<3;i++)f.events.push({sequence:f.events.length+1,event:{update:i===0?{type:'message_started',id:'new-output'}:i===1?{type:'text_delta',text:'New output while reading'}:{type:'message_finished'}}});await f.pages.refresh(['tasks']);
  });
  assert.equal(await page.getByText('New output while reading',{exact:true}).count(),0,'Live updates preserve the historical reading window');
  await page.locator('.task-composer-area .latest-button').click();
  await page.waitForFunction(()=>document.querySelector('.task-transcript')?.textContent.includes('New output while reading'));
  assert.deepEqual(errors,[]);
  console.log('Task history: automatic 80-row pages, single flight, stable anchor, live reading and Latest passed');
}

export async function taskHistoryEdges(page,{origin}){
  await page.goto(origin+'/scripts/tests/fixtures/task-history-edges.html');
  await page.waitForFunction(()=>window.historyEdges?.calls.length===1);
  await page.getByRole('button',{name:'Retry',exact:true}).waitFor();
  for(let i=0;i<8;i++)await page.evaluate(()=>new Promise(requestAnimationFrame));
  assert.equal(await page.evaluate(()=>historyEdges.calls.length),1,'Failed automatic history waits for explicit retry');
  await page.getByRole('button',{name:'Retry',exact:true}).click();
  await page.waitForFunction(()=>historyEdges.calls.length===2&&historyEdges.state.has_more===false);
  assert.equal(await page.evaluate(()=>historyEdges.maximum),1);
  await page.evaluate(()=>historyEdges.reset('latest'));
  await page.waitForFunction(()=>historyEdges.pending);
  await page.locator('.latest-button').click();
  assert.equal(await page.evaluate(()=>historyEdges.calls.at(-1).signal.aborted),true,'Latest cancels an in-flight history request');
  await page.evaluate(()=>historyEdges.release());
  await page.evaluate(()=>historyEdges.reset('run'));
  await page.waitForFunction(()=>historyEdges.pending);
  await page.evaluate(()=>historyEdges.changeRun());
  assert.equal(await page.evaluate(()=>historyEdges.calls.at(-1).signal.aborted),true,'A new run cancels old history IO');
  await page.evaluate(()=>historyEdges.release());
  await page.evaluate(()=>historyEdges.reset('dispose'));
  await page.waitForFunction(()=>historyEdges.pending);
  await page.evaluate(()=>historyEdges.ui.dispose());
  assert.equal(await page.evaluate(()=>historyEdges.calls.at(-1).signal.aborted),true,'Disposal cancels history IO');
  await page.evaluate(()=>historyEdges.release());
  console.log('Task history edges: short initial window loads automatically, failures require Retry, single flight, Latest/run/disposal cancellation passed');
}
