import assert from 'node:assert/strict';

// Multiple native offsets may arrive before the destination range is prepared.
// The empty destination shows a non-interactive spinner, never stale messages.
export async function preparedScrollLoading(browser,origin){
 const results=[];
 for(const direction of [-1,1]){
  const page=await browser.newPage({viewport:{width:direction<0?1280:390,height:800}}),errors=[];page.on('pageerror',error=>errors.push(error.message));
  try{
   await page.goto(origin+'/empty-prepared-fixture');
   await page.evaluate(async()=>{
    const {createMessageWindow}=await import('/components/message-window.mjs');
    const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/styles.css';document.head.append(sheet);await new Promise(resolve=>sheet.onload=resolve);
    window.scrollLocale='en';window.rangeGate=null;
    document.body.innerHTML='<style>.history{height:360px;width:600px;max-width:100%;overflow:auto;overflow-anchor:none;padding:0;position:relative}.message{height:100px!important;margin:0!important;flex:none}</style><div class="history"><div class="messages"></div></div>';
    const scroller=document.querySelector('.history'),host=scroller.firstElementChild;
    const rows=Array.from({length:100},(_,i)=>({id:'m'+i,viewKey:'m'+i,text:'Message '+i}));
    const list=createMessageWindow({scroller,host,locale:()=>scrollLocale,render(message,old){const record=old||{row:document.createElement('article')};record.row.className='message';record.row.textContent=message.text;if(rangeGate){const image=document.createElement('img');image.style.cssText='position:absolute;width:1px;height:1px';Object.defineProperty(image,'complete',{get:()=>false});image.decode=()=>rangeGate;record.row.append(image);}return record;},dispose:record=>record.row.remove()});
    list.setMessages(rows);
    const visible=()=>{const viewport=scroller.getBoundingClientRect();return [...host.querySelectorAll('article')].map(row=>{const box=row.getBoundingClientRect();return {id:row.dataset.messageId,offset:box.top-viewport.top,visible:Math.min(box.bottom,viewport.bottom)-Math.max(box.top,viewport.top),held:row.hasAttribute('data-conversation-hold')};}).filter(row=>row.visible>=24);};
    window.scrollLoading={list,scroller,visible};
   });
   await page.waitForFunction(()=>scrollLoading.list.prepared);
   await page.evaluate(()=>scrollLoading.list.restore({mode:'reading',position:{top:4000,anchors:[{key:'message:m40',offset:0}],anchor:{id:'m40',offset:0}}}));
   await page.waitForFunction(()=>scrollLoading.list.prepared&&Math.abs(scrollLoading.scroller.scrollTop-4000)<1);
   const result=await page.evaluate(direction=>{
    window.rangeGate=new Promise(resolve=>window.releaseRange=resolve);
    const {scroller,visible}=scrollLoading;
    const move=delta=>{scroller.dispatchEvent(new WheelEvent('wheel',{deltaY:delta,bubbles:true}));scroller.scrollTop+=delta;scroller.dispatchEvent(new Event('scroll'));};
    scrollLoading.move=move;const committed=visible();move(direction*400);const lastVisible=visible();
    const height=scroller.scrollHeight;move(direction*3000);const blank=visible(),indicator=document.querySelector('.conversation-range-loading');
    return {committed,lastVisible,blank,height,afterHeight:scroller.scrollHeight,loading:!!indicator};
   },direction);
   assert.notDeepEqual(result.lastVisible.map(row=>row.id),result.committed.map(row=>row.id),'Input moves beyond the last committed viewport');
   assert.deepEqual(result.blank,[],'Unprepared destination does not redisplay old rows');
   assert(result.loading);assert.equal(result.height,result.afterHeight,'Indicator does not change scroll extent');
   await page.waitForTimeout(280);
   const spinner=page.locator('.conversation-range-loading');
   assert.equal(await spinner.getAttribute('aria-label'),'Loading messages…');
   assert.equal(await spinner.evaluate(node=>getComputedStyle(node).opacity),'1');
   assert.equal(await spinner.evaluate(node=>getComputedStyle(node).pointerEvents),'none');
   assert(await spinner.evaluate(node=>{const box=node.getBoundingClientRect(),scroller=scrollLoading.scroller,viewport=scroller.getBoundingClientRect();return Math.abs(box.left+box.width/2-viewport.left-scroller.clientWidth/2)<1&&Math.abs(box.top+box.height/2-viewport.top-scroller.clientHeight/2)<1;}),'Indicator remains centered inside the scroller');
   await page.emulateMedia({reducedMotion:'reduce'});
   assert.equal(await spinner.evaluate(node=>getComputedStyle(node,'::before').animationName),'none');
   await page.evaluate(()=>{scrollLocale='zh-CN';scrollLoading.move(0);});
   assert.equal(await spinner.getAttribute('aria-label'),'正在加载消息…');
   await page.evaluate(direction=>scrollLoading.move(-direction*3000),direction);
   assert.equal(await spinner.count(),0,'Returning to mounted content removes loading immediately');
   await page.evaluate(direction=>{scrollLoading.move(direction*3000);releaseRange();rangeGate=null;},direction);
   await page.waitForFunction(()=>scrollLoading.list.prepared&&!document.querySelector('.conversation-range-loading'));
   assert.deepEqual(errors,[]);results.push({direction,...result});await page.evaluate(()=>scrollLoading.list.close());
  }finally{await page.close();}
 }
 return results;
}
