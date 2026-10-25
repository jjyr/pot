// Exercise the real flow adapter with delayed/repeated ResizeObserver delivery.
// Trace native writes in the fixture, without a product debug API.
async page=>{
  const origin=new URL(page.url()).origin,assert=(ok,label)=>{if(!ok)throw Error(label);},errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.addInitScript(()=>{
    const Native=ResizeObserver,held=[];
    window.measurementDelivery={hold:false,held,release(){const batches=held.splice(0).reverse();for(const [run,entries,observer]of batches){run(entries,observer);run(entries,observer);}}};
    window.ResizeObserver=class extends Native{constructor(run){super((entries,observer)=>{if(measurementDelivery.hold)held.push([run,entries,observer]);else run(entries,observer);});}};
  });
  await page.route('**/controller-fixture',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html lang="en"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><style>body{margin:0}.controlled-history{width:600px;max-width:100%;height:360px;overflow:auto;overflow-anchor:none;margin:24px 0}.controlled-row{flex:none!important;margin:0 0 8px!important;box-sizing:border-box;overflow:hidden}</style><div class="controlled-history" tabindex="0"><div id="header" data-scroll-anchor="heading" style="height:30px">Synthetic conversation</div><div class="message-window"></div></div></html>'}));
  await page.setViewportSize({width:900,height:700});await page.goto(origin+'/controller-fixture');
  await page.evaluate(async()=>{
    const {createMessageWindow}=await import('/components/message-window.mjs');
    const {readingAnchor}=await import('/components/reading-position.mjs');
    const scroller=document.querySelector('.controlled-history'),host=scroller.querySelector('.message-window'),writes=[],states=[],inputs=[];
    let rows=Array.from({length:100},(_,index)=>({id:'r'+index,viewKey:'r'+index,role:'assistant',height:80+index%5*20,text:'Synthetic message '+index})),phase='initial',restoreCalls=0;
    const descriptor=Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop');
    Object.defineProperty(scroller,'scrollTop',{get(){return descriptor.get.call(this);},set(top){const from=descriptor.get.call(this);descriptor.set.call(this,top);if(Math.abs(this.scrollTop-from)>.5)writes.push({phase,from,to:this.scrollTop,height:this.scrollHeight,kind:new Error().stack.includes('applyScrollAdjustment')?'geometry':'navigation'});}});
    // Known initial sizes isolate one controlled delta from unrelated estimate
    // rebases. The later inserted row deliberately has no cached measurement.
    const heights=new Map(rows.map(message=>[message.viewKey,message.height+8]));
    const list=createMessageWindow({scroller,host,heights,scrolled:delta=>inputs.push({phase,delta}),requestRender:()=>list.setMessages(rows),render(message,old){const record=old||{row:document.createElement('article')};record.row.className='message controlled-row';record.row.dataset.sequence=message.id.slice(1);record.row.style.height=message.wrap?'auto':message.height+'px';record.row.textContent=message.text;return record;},dispose:record=>record.row.remove()});
    list.subscribeReading(state=>states.push({phase,state,anchor:readingAnchor(scroller)}));list.setMessages(rows);
    const row=id=>host.querySelector(`[data-message-id="${id}"]`);
    const sample=()=>({top:scroller.scrollTop,height:scroller.scrollHeight,offset:row('r40')?.getBoundingClientRect().top-scroller.getBoundingClientRect().top,anchor:readingAnchor(scroller),state:list.snapshot(),mounted:host.querySelectorAll('article').length});
    window.controllerScene={list,scroller,writes,states,inputs,sample,
      phase(value){phase=value;},restore(){restoreCalls++;list.restore({mode:'reading',position:{top:0,anchors:[{key:'message:r40',offset:0}],anchor:{id:'r40',offset:0,sequence:'40'}}});},get restoreCalls(){return restoreCalls;},
      grow(amount,{immutable=false,layout=true}={}){const index=rows.findIndex(item=>item.id==='r38');if(immutable)rows=rows.map((item,i)=>i===index?{...item,height:item.height+amount}:item);else{rows[index].height+=amount;row('r38').style.height=rows[index].height+'px';}if(layout)list.setMessages(rows);},
      repeat(){for(let i=0;i<5;i++)list.setMessages(rows);},
      insert(){const index=rows.findIndex(item=>item.id==='r39');rows=[...rows.slice(0,index),{id:'inserted',viewKey:'inserted',role:'assistant',height:1500,text:'A newly mounted long row'},...rows.slice(index)];list.setMessages(rows);},
      prepend(){rows=[...Array.from({length:8},(_,i)=>({id:'old'+i,viewKey:'old'+i,role:'user',height:70,text:'Earlier '+i})),...rows];list.setMessages(rows);},
      reorder(){const anchor=rows.find(item=>item.id==='r40'),others=rows.filter(item=>item!==anchor).reverse(),index=Math.floor(others.length/2);rows=[...others.slice(0,index),anchor,...others.slice(index)];list.setMessages(rows);},
      trim(){rows=rows.filter(item=>['r40','r41'].includes(item.id));list.setMessages(rows);},
      wrap(){const item=rows.find(item=>item.id==='r38');rows=rows.map(row=>row===item?{...item,wrap:true,text:'A wrapping synthetic line. '.repeat(90)}:row);list.setMessages(rows);},
    };
  });
  const settle=async()=>{await page.waitForTimeout(90);};
  const sample=()=>page.evaluate(()=>controllerScene.sample());
  const phase=name=>page.evaluate(name=>{controllerScene.phase(name);controllerScene.writes.length=0;},name);
  const offset=async expected=>{const current=await sample();assert(Math.abs(current.offset-expected)<=.6,`Expected reading offset ${expected}, got ${current.offset}`);return current;};
  await settle();await page.evaluate(()=>controllerScene.restore());await settle();await offset(0);
  const timeline=[];
  await page.evaluate(()=>{measurementDelivery.hold=true;measurementDelivery.held.length=0;});
  for(const immutable of [false,true]){
    await phase(immutable?'initial-measure-first':'dom-before-RO');const before=await sample(),amount=immutable?90:120;
    await page.evaluate(({amount,immutable})=>{controllerScene.grow(amount,{immutable});controllerScene.repeat();},{amount,immutable});await settle();await offset(0);
    const first=await page.evaluate(()=>controllerScene.writes.slice());
    assert(first.length===1&&Math.abs(first[0].to-first[0].from-amount)<=.6,'One height delta receives one geometry correction before RO: '+JSON.stringify({before,first,after:await sample()}));
    await page.evaluate(()=>{measurementDelivery.release();controllerScene.repeat();});await settle();await offset(0);
    const after=await sample();assert(Math.abs(after.top-before.top-amount)<=.6,'Repeated/reordered measurements do not add the same height delta again');
    assert((await page.evaluate(()=>controllerScene.writes.length))===first.length,'Late duplicate RO and repeated layout make no additional position decision');
    timeline.push({reason:immutable?'first measure + late RO':'DOM + repeated layout + late RO',delta:amount,from:before.top,to:after.top,offset:after.offset,writes:first});
  }
  // New rows still have an immediate measured size, but only this controller
  // compensates it. A duplicate observer delivery cannot initiate a restore.
  await phase('first mount');await page.evaluate(()=>controllerScene.insert());await settle();await offset(0);
  const mountedTop=(await sample()).top,writes=await page.evaluate(()=>controllerScene.writes.length);
  await page.evaluate(()=>{measurementDelivery.release();controllerScene.repeat();});await settle();await offset(0);
  assert((await sample()).top===mountedTop&&(await page.evaluate(()=>controllerScene.writes.length))===writes,'New row initial measurement and RO share one intent');
  // Native input interleaved with unreported DOM growth must translate the
  // committed anchor by the input delta, not adopt the whole shifted DOM.
  const box=await page.locator('.controlled-history').boundingBox();await page.mouse.move(box.x+box.width-18,box.y+180);
  await phase('wheel between DOM and RO');const beforeWheel=await sample();
  await page.evaluate(()=>controllerScene.grow(80,{layout:false}));await page.mouse.wheel(0,-36);await settle();const wheel=await offset(36);
  assert(Math.abs(wheel.top-beforeWheel.top-(80-36))<=.6,'Only the real wheel delta changes reading intent');
  await page.evaluate(()=>{measurementDelivery.release();controllerScene.repeat();});await settle();await offset(36);
  await phase('key between DOM and RO');await page.locator('.controlled-history').focus();const keyStart=await sample();
  await page.evaluate(()=>controllerScene.grow(50,{layout:false}));await page.keyboard.press('ArrowUp');await page.waitForTimeout(300);const key=await sample();
  const keyInput=await page.evaluate(()=>controllerScene.inputs.filter(input=>input.phase==='key between DOM and RO').reduce((sum,input)=>sum+input.delta,0));
  assert(keyInput<0,'The real ArrowUp generated native movement');
  assert(Math.abs(key.offset-(keyStart.offset-keyInput))<=1,'Keyboard motion is adopted throughout its native scrolling tail: '+JSON.stringify({keyStart,key,keyInput,writes:await page.evaluate(()=>controllerScene.writes)}));
  assert(Math.abs(key.top-keyStart.top-(50+keyInput))<=1,'Key input and geometry use distinct deltas');
  await page.evaluate(()=>{measurementDelivery.release();controllerScene.repeat();});await settle();await offset(key.offset);
  timeline.push({reason:'wheel + pending height',input:-36,heightDelta:80,from:beforeWheel.top,to:wheel.top,offset:wheel.offset},{reason:'key + pending height',input:keyInput,heightDelta:50,from:keyStart.top,to:key.top,offset:key.offset});
  // A real CDP touch holds geometry corrections without a second cursor timer.
  const cdp=await page.context().newCDPSession(page);await cdp.send('Emulation.setTouchEmulationEnabled',{enabled:true,maxTouchPoints:1});
  const point={x:box.x+box.width/2,y:box.y+150};
  await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[point]});
  await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{...point,y:point.y+55}]});await page.waitForTimeout(120);
  await phase('height during touch');const touch=await sample();await page.evaluate(()=>controllerScene.grow(70,{immutable:true}));await settle();
  const held=await sample();assert(Math.abs(held.offset-touch.offset)<=1,'Held touch compensation keeps the same content offset');
  assert(held.top===touch.top&&(await page.evaluate(()=>controllerScene.writes.length))===0,'Contact defers the native geometry write');
  await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await page.waitForTimeout(350);await offset(touch.offset);
  const released=await sample();assert(Math.abs(released.top-touch.top-70)<=1,'The held geometry delta flushes once after touch');
  await page.evaluate(()=>{measurementDelivery.release();controllerScene.repeat();});await settle();await offset(touch.offset);
  timeline.push({reason:'touch + pending height',heightDelta:70,from:touch.top,to:released.top,offset:released.offset});
  await page.evaluate(()=>controllerScene.restore());await settle();
  await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[point]});
  await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{...point,y:point.y+55}]});await page.waitForTimeout(120);
  await page.evaluate(()=>controllerScene.grow(30,{immutable:true}));await settle();
  await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  await page.evaluate(()=>controllerScene.list.scrollToLatest());await page.waitForTimeout(400);
  const latest=await sample();assert(latest.state.mode==='latest'&&latest.height-360-latest.top<=1,'Explicit Latest takes priority over a held touch correction and its momentum tail');
  await page.evaluate(()=>measurementDelivery.release());await settle();
  assert((await sample()).height-360-(await sample()).top<=1,'Late measurement cannot replay the retired touch reading point');
  await cdp.detach();
  await page.evaluate(()=>{measurementDelivery.hold=false;controllerScene.restore();});await settle();await offset(0);
  const restores=await page.evaluate(()=>controllerScene.restoreCalls);await page.evaluate(()=>controllerScene.repeat());await settle();
  assert(await page.evaluate(()=>controllerScene.restoreCalls)===restores,'Range/layout updates never request another page restoration');
  await phase('prepend');await page.evaluate(()=>controllerScene.prepend());await settle();await offset(0);
  await phase('width reflow');await page.evaluate(()=>{controllerScene.wrap();controllerScene.scroller.style.width='320px';});await settle();await offset(0);
  const reflowedBox=await page.locator('.controlled-history').boundingBox();await page.mouse.move(reflowedBox.x+reflowedBox.width-18,reflowedBox.y+180);
  await phase('target interrupt');await page.evaluate(()=>controllerScene.list.locate({id:'r40'}));await settle();await page.mouse.wheel(0,-36);await settle();
  assert(await page.evaluate(()=>controllerScene.list.destination===null),'Real input interrupts explicit target navigation');
  const reading=await sample();await page.evaluate(()=>controllerScene.repeat());await settle();assert(Math.abs((await sample()).offset-reading.offset)<=.6,'No stale target command replays after input');
  await page.evaluate(()=>controllerScene.restore());await settle();await offset(0);
  await phase('reorder');await page.evaluate(()=>controllerScene.reorder());await settle();await offset(0);
  await phase('trim');await page.evaluate(()=>controllerScene.trim());await settle();
  const trimmed=await sample();assert(trimmed.state.mode==='reading','Geometry clamping at the boundary does not enable following');
  assert(trimmed.top===0&&trimmed.offset===30,'A short trimmed list clamps at zero; the header makes the original zero offset unreachable');
  assert(trimmed.mounted===2,'Trimming retires removed rows');
  assert(errors.length===0,errors.join('; '));
  await page.evaluate(()=>controllerScene.list.close());
  return {passed:true,timeline,restores,boundary:{top:trimmed.top,expectedTop:0,offset:trimmed.offset,expectedOffset:30,height:trimmed.height,mode:trimmed.state.mode},errors};
}
