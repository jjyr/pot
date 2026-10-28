import assert from 'node:assert/strict';

// An in-window reference keeps its source control focused while the controller
// reads another row. Reordering that pinned row must not become native input.
export async function preparedFocusedPins(browser,origin){
  const results=[];
  for(const atomic of [true,false]){
    const context=await browser.newContext(),page=await context.newPage(),errors=[],evidence={atomic};
    page.on('pageerror',error=>errors.push(error.message));
    try{
      await page.goto(origin+'/empty-prepared-fixture');
      await page.evaluate(async atomic=>{
        if(!atomic)Object.defineProperty(Element.prototype,'moveBefore',{value:undefined,configurable:true});
        const {createMessageWindow}=await import('/components/message-window.mjs');
        const {preparedText,retirePreparedTree}=await import('/components/prepared-render.mjs');
        document.body.replaceChildren();const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/styles.css';document.head.append(sheet);await new Promise((resolve,reject)=>{sheet.onload=resolve;sheet.onerror=reject;});await document.fonts.ready;
        const scroller=document.createElement('div');scroller.className='history';scroller.style.cssText='width:500px;height:520px;flex:none';
        const column=document.createElement('div');column.className='message-column';const region=document.createElement('div'),host=document.createElement('div');host.className='message-window';column.append(region,host);scroller.append(column);
        const after=document.createElement('button');after.id='after';after.textContent='After transcript';document.body.append(scroller,after);
        const message=n=>({id:'m'+n,viewKey:'m'+n,text:('Measured row '+n+'. ').repeat(24)});
        let rows=Array.from({length:80},(_,i)=>message(i+81)),revision=0,ui;
        const draw=()=>ui.setMessages(rows,{regions:[{key:'notice',host:region,revision,render:target=>{target.textContent=revision?'Earlier rows committed':'History pending';}}]});
        ui=createMessageWindow({scroller,host,requestRender:draw,dispose:entry=>retirePreparedTree(entry.row),render:(message,entry)=>{
          if(!entry){const row=document.createElement('article');row.className='message';const body=document.createElement('div');body.className='message-body';const prose=document.createElement('p');body.append(prose);row.append(body);entry={row,prose};if(['m100','m120'].includes(message.id)){const destination=message.id==='m100'?'m120':'m100',nested=document.createElement('section'),button=document.createElement('button');button.textContent='Read row '+destination.slice(1);button.onclick=()=>ui.locate({id:destination});nested.append(button);body.append(nested);}}
          preparedText(entry.prose,'prose',message.text);return entry;
        }});
        const sample=()=>{const row=host.querySelector('[data-message-id="m100"]'),box=row?.getBoundingClientRect(),viewport=scroller.getBoundingClientRect();return {prepared:ui.prepared,position:ui.snapshot(),top:scroller.scrollTop,offset:box?.top-viewport.top,desired:box&&(scroller.clientHeight-box.height)/2,visible:box?Math.min(box.bottom,viewport.bottom)-Math.max(box.top,viewport.top):0,focus:document.activeElement?.closest('article')?.dataset.messageId,order:[...host.children].filter(node=>node.dataset.virtualIndex).map(node=>Number(node.dataset.virtualIndex))};};
        window.focusPins={ui,host,after,sample,locate:()=>ui.locate({id:'m100'}),prepend(){revision++;rows=[...Array.from({length:80},(_,i)=>message(i+1)),...rows];draw();},close(){ui.close();}};draw();
      },atomic);
      await page.waitForFunction(()=>focusPins.ui.prepared);await page.evaluate(()=>focusPins.locate());
      await page.getByRole('button',{name:'Read row 120',exact:true}).click();
      await page.getByRole('button',{name:'Read row 100',exact:true}).click();
      await page.waitForFunction(()=>{const s=focusPins.sample();return s.prepared&&s.visible>=24&&s.position.id==='m100';},null,{timeout:5000});
      const before=await page.evaluate(()=>{window.pinnedControl=focusPins.host.querySelector('[data-message-id="m120"] button');return focusPins.sample();});
      evidence.before=before;
      assert.equal(before.focus,'m120');await page.evaluate(()=>focusPins.prepend());
      await page.waitForFunction(()=>focusPins.ui.prepared);
      const samples=await page.evaluate(async()=>{const samples=[];for(let i=0;i<8;i++){await new Promise(requestAnimationFrame);samples.push({...focusPins.sample(),sameFocus:document.activeElement===pinnedControl});}return samples;});
      evidence.samples=samples;
      assert(samples.some(s=>s.prepared),'The measured destination preparation completes');
      assert(samples.every(s=>s.visible>=24&&s.position.id==='m100'&&s.sameFocus),'The latest destination stays readable with the original source control focused');
      assert(samples.every(s=>Math.abs(s.offset-before.offset)<=3),'The measured target retains the existing continuity bound');
      assert(samples.every(s=>Math.abs(s.offset-s.desired)<=.6),'Each native landing stays within the controller residual bound');
      assert(samples.every(s=>s.order.every((value,index)=>index===0||value>s.order[index-1])),'Physical roots stay in their real virtual-index order');
      await page.keyboard.press('Tab');assert(await page.evaluate(()=>document.activeElement===focusPins.after),'Tab follows the retained live DOM order');
      await page.evaluate(()=>focusPins.close());assert.deepEqual(errors,[]);results.push({atomic,before,samples,tabAfter:true});
    }catch(error){error.evidence={...evidence,errors};throw error;}finally{await context.close();}
  }
  return results;
}
