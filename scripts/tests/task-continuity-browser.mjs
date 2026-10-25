import assert from 'node:assert/strict';

// The real Task page and repository, not a second virtualizer implementation.
export async function taskContinuity(page,{origin}) {
  const results=[];
  for(const width of [1280,390,320]){
    await page.setViewportSize({width,height:800});
    await page.goto(origin+'/scripts/tests/fixtures/db-driven-task.html?mixed=1');
    await page.waitForFunction(()=>document.querySelector('#results')?.textContent.startsWith('Ready'));
    await page.evaluate(()=>{
      const sample=()=>{
        const h=document.querySelector('.task-history'),top=h.getBoundingClientRect().top;
        const rows=[...h.querySelectorAll('article.message')],visible=rows.flatMap(row=>{
          const box=row.getBoundingClientRect();return box.height&&box.bottom>top&&box.top<top+h.clientHeight?[{id:row.dataset.messageId,sequence:Number(row.dataset.messageId.match(/row-(\d+)$/)?.[1])+1,top:box.top-top,bottom:box.bottom-top}]:[];
        });
        return {top:h.scrollTop,height:h.scrollHeight,viewport:h.clientHeight,mounted:rows.length,visible,requests:taskFixture.requests.length};
      };
      window.taskContinuity={sample,last:[]};
    });
    const sample=()=>page.evaluate(()=>taskContinuity.sample());
    const settle=()=>page.evaluate(async()=>{
      let previous,stable=0;
      for(let frame=0;frame<90;frame++){
        await new Promise(requestAnimationFrame);const state=taskContinuity.sample(),key=JSON.stringify(state);
        stable=key===previous?stable+1:0;previous=key;if(stable===3)return;
      }
      throw Error('Task viewport did not settle: '+JSON.stringify(taskContinuity.sample()));
    });
    const wheel=async(delta)=>{
      const previous=await sample();await page.mouse.wheel(0,delta);
      await page.waitForFunction(({top,delta})=>{const h=document.querySelector('.task-history');return h.scrollTop!==top||(delta<0?h.scrollTop===0:h.scrollHeight-h.clientHeight-h.scrollTop<=1);},{top:previous.top,delta});
      await settle();return sample();
    };
    await settle();await page.locator('.task-history').hover();
    let previous=await sample(),worstDrift=0,worstSample,steps=0,maxMounted=0;
    const requests=previous.requests,seen={up:new Set(),down:new Set()},witness=(state,direction)=>state.visible.forEach(row=>{if(Math.min(row.bottom,state.viewport)-Math.max(0,row.top)>=24)seen[direction].add(row.sequence);});
    for(const direction of ['up','down'])for(let step=0;step<160;step++){
      witness(previous,direction);const delta=direction==='up'?-36:36,next=await wheel(delta);steps++;
      await page.evaluate(state=>{taskContinuity.last.push(state);if(taskContinuity.last.length>2)taskContinuity.last.shift();},{direction,step,delta,previous,next});
      const common=previous.visible.find(row=>next.visible.some(item=>item.id===row.id));
      assert.ok(common,`${width}px Task ${direction} step ${step} skipped its visible interval`);
      const actual=next.visible.find(row=>row.id===common.id).top-common.top;
      const expected=delta>0?Math.min(delta,previous.height-previous.viewport-previous.top):Math.max(delta,-previous.top);
      const drift=Math.abs(actual+expected),measurement={step,direction,id:common.id,wheelDelta:delta,boundaryExpected:expected,scrollDelta:next.top-previous.top,anchorMovement:actual,drift};
      assert.ok(drift<=3,`${width}px Task same-message displacement: `+JSON.stringify(measurement));
      if(!worstSample||drift>worstDrift)worstSample=measurement;worstDrift=Math.max(worstDrift,drift);maxMounted=Math.max(maxMounted,next.mounted);
      witness(next,direction);previous=next;
      if(Array.from({length:19},(_,i)=>i+1181).every(id=>seen[direction].has(id))&&(direction==='up'||next.height-next.viewport-next.top<=2))break;
    }
    for(const direction of ['up','down'])for(let id=1181;id<=1199;id++)assert.ok(seen[direction].has(id),`${width}px Task ${direction}: message ${id} was never readable`);
    assert.equal(previous.requests,requests,'Task local traversal does not replay its event prefix');assert.ok(maxMounted<120,'Task retains bounded message DOM');
    for(let step=0;step<12;step++)await wheel(-36);
    const reading=(await sample()).visible[0];
    await page.evaluate(async()=>{const f=taskFixture;for(const update of [{type:'message_started',id:'task-live'},{type:'text_delta',text:'New Task output while reading'},{type:'message_finished'}])f.events.push({sequence:f.events.length+1,event:{update}});await f.pages.refresh(['tasks']);});
    await settle();const retained=(await sample()).visible.find(row=>row.id===reading.id);
    assert.ok(retained&&Math.abs(retained.top-reading.top)<=3,'Task output retains the historical message identity and anchor');
    await page.getByRole('button',{name:'Latest message',exact:true}).click();await page.getByText('New Task output while reading',{exact:true}).waitFor();await settle();
    const end=await sample();assert.ok(end.height-end.viewport-end.top<=2,'Task Latest explicitly follows new output');
    results.push({width,steps,worstDrift,worstSample,maxMounted,readable:[1181,1199]});
  }
  return {passed:true,results};
}
