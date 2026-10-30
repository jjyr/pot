import assert from 'node:assert/strict';

// The real Task page and repository, not a second virtualizer implementation.
export async function taskContinuity(page,{origin}) {
  const results=[];
  for(const width of [1280]){
    await page.setViewportSize({width,height:800});
    await page.goto(origin+'/scripts/tests/fixtures/db-driven-task.html?mixed=1');
    await page.waitForFunction(()=>document.querySelector('#results')?.textContent.startsWith('Ready'));
    await page.evaluate(()=>{
      const sample=()=>{
        const h=document.querySelector('.task-history:not([data-preparation-surface])'),top=h.getBoundingClientRect().top;
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
      await page.waitForFunction(({top,delta})=>{const h=document.querySelector('.task-history:not([data-preparation-surface])');return h.scrollTop!==top||(delta<0?h.scrollTop===0:h.scrollHeight-h.clientHeight-h.scrollTop<=1);},{top:previous.top,delta});
      await settle();return sample();
    };
    await settle();await page.locator('.task-history:not([data-preparation-surface])').hover();
    const {steps,worstDrift,worstSample,maxMounted}=await witnessMessages({page,sample,wheel,width,ids:Array.from({length:11},(_,i)=>i+1189),limit:90,label:'Task',bounded:120});
    for(let step=0;step<12;step++)await wheel(-36);
    const reading=(await sample()).visible[0];
    await page.evaluate(async()=>{const f=taskFixture;for(const update of [{type:'message_started',id:'task-live'},{type:'text_delta',text:'New Task output while reading'},{type:'message_finished'}])f.events.push({sequence:f.events.length+1,event:{update}});await f.pages.refresh(['tasks']);});
    await settle();const retained=(await sample()).visible.find(row=>row.id===reading.id);
    assert.ok(retained&&Math.abs(retained.top-reading.top)<=3,'Task output retains the historical message identity and anchor');
    await page.getByRole('button',{name:'Latest message',exact:true}).click();await page.getByText('New Task output while reading',{exact:true}).waitFor();await settle();
    const end=await sample();assert.ok(end.height-end.viewport-end.top<=2,'Task Latest explicitly follows new output');
    results.push({width,steps,worstDrift,worstSample,maxMounted,readable:[1189,1199]});
  }
  return {passed:true,results};
}

// One controller contract; Chat and Task supply their actual DOM samples and
// trusted wheel input. Task uses a smaller desktop integration dataset; the
// phone Chat trace owns the intensive mixed/async/nested geometry coverage.
export async function witnessMessages({page,sample,wheel,width,ids,limit=160,label,bounded,retainedCount}){
  let previous=await sample(),worstDrift=0,worstSample,steps=0,maxMounted=0;
  const initialRequests=previous.requests,seen={up:new Set(),down:new Set()};
  const witness=(state,direction)=>state.visible.forEach(row=>{if(Math.min(row.bottom,state.viewport)-Math.max(row.top,0)>=24)seen[direction].add(Number(row.sequence));});
  for(const direction of ['up','down'])for(let step=0;step<limit;step++){
    witness(previous,direction);const delta=direction==='up'?-36:36,next=await wheel(delta);steps++;
    const common=previous.visible.find(row=>next.visible.some(item=>item.id===row.id));
    if(retainedCount!==undefined)assert.equal(next.mounted,retainedCount,'Existing loaded rows stay mounted during every wheel step');
    const state={label,width,direction,step,delta,previous,next};
    await page.evaluate(state=>{window.wheelFrames||=[];wheelFrames.push(state);if(wheelFrames.length>2)wheelFrames.shift();},state);
    assert.ok(common,`${label} skipped its visible interval: `+JSON.stringify(state));
    const actual=next.visible.find(row=>row.id===common.id).top-common.top;
    const expected=delta>0?Math.min(delta,previous.height-previous.viewport-previous.top):Math.max(delta,-previous.top);
    const drift=Math.abs(actual+expected),measurement={step,direction,id:common.id,wheelDelta:delta,boundaryExpected:expected,scrollDelta:next.top-previous.top,anchorMovement:actual,drift};
    assert.ok(drift<=3,`${label} same-message displacement: `+JSON.stringify(measurement));
    if(!worstSample||drift>worstDrift)worstSample=measurement;worstDrift=Math.max(worstDrift,drift);maxMounted=Math.max(maxMounted,next.mounted);
    witness(next,direction);previous=next;
    if(ids.every(id=>seen[direction].has(id))&&(direction==='up'||next.height-next.viewport-next.top<=2))break;
  }
  for(const direction of ['up','down'])for(const id of ids)assert.ok(seen[direction].has(id),`${label} ${width}px ${direction}: message ${id} was never readable`);
  if(initialRequests!==undefined)assert.equal(previous.requests,initialRequests,'Local traversal must not replay events');
  if(retainedCount!==undefined)assert.equal(maxMounted,retainedCount,'Retain every loaded message DOM row');
  else assert.ok(maxMounted<bounded,'Retain bounded message DOM');
  return {steps,worstDrift,worstSample,maxMounted};
}
