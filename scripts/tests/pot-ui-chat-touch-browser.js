// Run with Playwright CLI against the loopback chat-scroll fixture in an iPhone
// context. Chromium sends trusted touch gestures; WebKit exercises the same
// drag/cancel/momentum sequence using browser-side native scroll input. Neither
// mode establishes behavior on a physical iPhone.
async page=>{
  const origin=new URL(page.url()).origin;
  await page.goto(`${origin}/scripts/tests/fixtures/chat-scroll.html?conversation=alpha&locale=zh-CN&theme=dark`);
  await page.waitForSelector('.history article.message');
  await page.evaluate(()=>{
    const history=document.querySelector('.history:not([data-preparation-surface])'),native=Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop');
    const trace=window.chatTouchTrace={writes:[],frames:[],events:[],touching:false,momentum:false,lastInput:performance.now(),simulating:false,active:true,errors:[],gestures:[]};
    Object.defineProperty(history,'scrollTop',{configurable:true,get(){return native.get.call(this);},set(value){
      const before=native.get.call(this);native.set.call(this,value);
      trace.writes.push({before,value,touching:trace.touching,momentum:trace.momentum,quietFor:performance.now()-trace.lastInput,stack:new Error().stack});
    }});
    window.addEventListener('error',event=>trace.errors.push(event.message));
    window.addEventListener('unhandledrejection',event=>trace.errors.push(String(event.reason?.stack||event.reason)));
    // Programmatic input in WebKit emits a separate native scrollend for each
    // simulated step. Suppress those and deliver one at the actual tail end.
    // Observe the boundary before TanStack's element listener commits held
    // corrections; writes inside scrollend are already outside moving momentum.
    const scrollend=event=>{if(event.target!==history)return;if(trace.simulating&&event.isTrusted)event.stopImmediatePropagation();else trace.momentum=false;};
    document.addEventListener('scrollend',scrollend,{capture:true});
    trace.close=()=>document.removeEventListener('scrollend',scrollend,{capture:true});
    let quiet;
    for(const name of ['touchstart','touchmove','touchend','touchcancel','pointercancel','scroll','scrollend'])history.addEventListener(name,event=>{
      if(name==='touchstart'){trace.touching=true;trace.momentum=false;}
      if(name==='touchend'||name==='touchcancel'){trace.touching=false;trace.momentum=true;}
      if(name==='scrollend')trace.momentum=false;
      if(name==='scroll'||name==='touchend')trace.lastInput=performance.now();
      if(trace.momentum){clearTimeout(quiet);quiet=setTimeout(()=>{trace.momentum=false;},150);}
      trace.events.push({name,trusted:event.isTrusted});
    },{passive:true,capture:true});
    trace.capture=()=>{
      const top=history.getBoundingClientRect().top;
      const row=[...history.querySelectorAll('article.message')].find(node=>{const box=node.getBoundingClientRect();return box.height&&box.bottom>top&&box.top<top+history.clientHeight;});
      const gap=[...history.querySelectorAll('.message-window-spacer')].some(node=>{const box=node.getBoundingClientRect();return box.height>1&&box.bottom>top+1&&box.top<top+history.clientHeight-1;});
      return {sequence:Number(row?.dataset.sequence||0),offset:row?row.getBoundingClientRect().top-top:0,top:history.scrollTop,gap};
    };
    const sample=()=>{if(!trace.active)return;trace.frames.push(trace.capture());requestAnimationFrame(sample);};requestAnimationFrame(sample);
  });
  let cdp;
  try{cdp=await page.context().newCDPSession(page);}catch{/* WebKit has no CDP touch input. */}
  const nativeTouch=!!cdp;
  try{
    const rect=await page.locator('.history:not([data-preparation-surface])').boundingBox(),x=rect.x+rect.width*.5,y=rect.y+rect.height*.5;
    const gesture=async(distance,speed=2200,quiet=300)=>{
      const before=await page.evaluate(()=>window.chatTouchTrace.capture());
      if(cdp){
        await cdp.send('Input.synthesizeScrollGesture',{x,y,yDistance:distance,speed,gestureSourceType:'touch',preventFling:false});
      }else{
        await page.evaluate(async distance=>{
          const history=document.querySelector('.history:not([data-preparation-surface])'),native=Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop');
          window.chatTouchTrace.simulating=true;
          const frame=()=>new Promise(requestAnimationFrame);
          const touch=(name,y)=>{const event=new Event(name,{bubbles:true});Object.defineProperty(event,'touches',{value:name==='touchend'?[]:[{clientY:y}]});history.dispatchEvent(event);};
          touch('touchstart',200);
          for(let i=0;i<24;i++){
            touch('touchmove',200+(i+1)*Math.sign(distance)*10);
            if(i===0)history.dispatchEvent(new PointerEvent('pointercancel',{bubbles:true,pointerType:'touch'}));
            native.set.call(history,history.scrollTop-distance/32);history.dispatchEvent(new Event('scroll'));await frame();
          }
          touch('touchend',0);
          for(let i=0;i<8;i++){native.set.call(history,history.scrollTop-distance/32);history.dispatchEvent(new Event('scroll'));await frame();}
          window.chatTouchTrace.simulating=false;history.dispatchEvent(new Event('scrollend'));
        },distance);
      }
      if(quiet)await page.waitForTimeout(quiet);
      await page.evaluate(({before,distance})=>{const t=window.chatTouchTrace;t.gestures.push({before,after:t.capture(),distance});},{before,distance});
    };
    // Reverse immediately, without waiting for the preceding momentum timer.
    for(const distance of [1800,2400,3200,-2400,-3200,3200,3200,-3200])await gesture(distance,2200,0);
    // Move beyond the 640-message data window, then return through evicted
    // history. These gestures also cross the bounded detached DOM cache.
    for(let i=0;i<24;i++){
      if(await page.evaluate(()=>window.chatTouchTrace.capture().sequence<=800))break;
      await gesture(12000,16000);
    }
    const oldest=await page.evaluate(()=>window.chatTouchTrace.capture().sequence);
    if(oldest>800)throw Error('Touch traversal did not cross the data window: '+oldest);
    for(let i=0;i<24;i++){
      if(await page.evaluate(()=>window.chatTouchTrace.capture().sequence>=1590))break;
      await gesture(-12000,16000);
    }
    if(await page.evaluate(()=>window.chatTouchTrace.capture().sequence<1590))throw Error('Touch traversal did not return to latest history');
    const result=await page.evaluate(nativeTouch=>{
      const t=window.chatTouchTrace;t.active=false;
      // TanStack's 150 ms tail timer can run before this trace's timer in the
      // same event-loop turn. Classify by elapsed quiet time as well, so a
      // settled correction is not mistaken for a write into moving momentum.
      return window.chatTouchResult={input:nativeTouch?'trusted Chromium touch':'WebKit simulated drag and momentum',frames:t.frames.length,blankFrames:t.frames.filter(frame=>!frame.sequence).length,gapFrames:t.frames.filter(frame=>frame.gap).length,trustedTouchMoves:t.events.filter(event=>event.name==='touchmove'&&event.trusted).length,pointerCancels:t.events.filter(event=>event.name==='pointercancel').length,writesDuringTouch:t.writes.filter(write=>write.touching).length,writesDuringTail:t.writes.filter(write=>write.momentum&&write.quietFor<150).length,deferredAfterQuiet:t.writes.filter(write=>write.momentum&&write.quietFor>=150).length,foreignWriters:t.writes.filter(write=>!write.stack.includes('scrollToFn')).length,errors:t.errors,gestures:t.gestures};
    },nativeTouch);
    if(result.writesDuringTouch||result.writesDuringTail||result.foreignWriters||result.blankFrames||result.gapFrames||result.errors.length)throw Error(JSON.stringify(result));
    if(nativeTouch&&!result.trustedTouchMoves)throw Error('No trusted touch movement was delivered');
    let stalled=false;
    for(const gesture of result.gestures){
      const progress=gesture.before.sequence-gesture.after.sequence;
      const waiting=Math.abs(progress)<2&&Math.abs(gesture.before.offset-gesture.after.offset)<100;
      if(waiting&&stalled)throw Error('Repeated long gestures made no progress: '+JSON.stringify(gesture));
      stalled=waiting;
      if(progress*Math.sign(gesture.distance)<-2)throw Error('Gesture moved in the opposite direction: '+JSON.stringify(gesture));
    }
    return {...result,gestures:result.gestures.length,oldest:Math.min(...result.gestures.map(gesture=>gesture.after.sequence)),latest:result.gestures.at(-1).after.sequence};
  }finally{
    await page.evaluate(()=>{if(window.chatTouchTrace){window.chatTouchTrace.active=false;window.chatTouchTrace.close();}});
    await cdp?.detach();
  }
}
