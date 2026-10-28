// Run with Playwright CLI against the loopback Chat fixture. Uses trusted
// textarea input and samples every painted frame, including resize callbacks.
async page=>{
  const origin=new URL(page.url()).origin;
  await page.goto(`${origin}/scripts/tests/fixtures/chat-scroll.html?conversation=alpha&locale=zh-CN&theme=dark`);
  await page.waitForSelector('.history article.message');
  await page.evaluate(()=>{
    const history=document.querySelector('.history:not([data-preparation-surface])'),native=Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop');
    const trace=window.chatComposerTrace={active:true,mode:'latest',frames:0,errors:[],writes:[],inputs:0,samples:[]};
    trace.geometry=()=>{
      const viewport=history.getBoundingClientRect(),rows=[...history.querySelectorAll('article.message')];
      const first=rows.find(row=>{const box=row.getBoundingClientRect();return box.height&&box.bottom>viewport.top&&box.top<viewport.top+history.clientHeight;});
      const last=rows.find(row=>row.dataset.messageId==='m1600');
      return {composer:document.querySelector('.composer-area').getBoundingClientRect().height,input:document.querySelector('.composer-input').getBoundingClientRect().height,viewport:history.clientHeight,bottom:viewport.bottom,last:last?.getBoundingClientRect().bottom,anchor:first?.dataset.messageId,offset:first?first.getBoundingClientRect().top-viewport.top:0,top:history.scrollTop,end:history.scrollHeight-history.clientHeight-history.scrollTop};
    };
    Object.defineProperty(history,'scrollTop',{configurable:true,get(){return native.get.call(this);},set(value){native.set.call(this,value);trace.writes.push(new Error().stack);}});
    document.querySelector('.composer-input').addEventListener('input',event=>{if(event.isTrusted)trace.inputs++;});
    window.addEventListener('error',event=>trace.errors.push(event.message));
    window.addEventListener('unhandledrejection',event=>trace.errors.push(String(event.reason?.stack||event.reason)));
    trace.validate=()=>{
      const current=trace.geometry();
      if(!current.anchor)throw Error('Composer resize exposed a blank history frame');
      if(trace.mode==='latest'){
        if(current.end>1||current.last===undefined||current.last>current.bottom+1||!document.querySelector('.latest-button').hidden)throw Error('Composer resize lost Latest: '+JSON.stringify(current));
      }else if(trace.mode==='reading'){
        const row=history.querySelector(`[data-message-id="${trace.anchor.anchor}"]`);
        const offset=row?.getBoundingClientRect().top-history.getBoundingClientRect().top;
        if(offset===undefined||Math.abs(offset-trace.anchor.offset)>2||document.querySelector('.latest-button').hidden)throw Error('Composer resize moved the historical reading anchor: '+JSON.stringify({expected:trace.anchor,current,offset}));
      }
      return current;
    };
    const sample=()=>{if(!trace.active)return;trace.frames++;try{trace.validate();}catch(error){trace.errors.push(error.message);}requestAnimationFrame(sample);};requestAnimationFrame(sample);
  });
  const input=page.locator('.composer-input');
  const settle=()=>page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>setTimeout(resolve,50)))));
  const edit=async(label,text)=>{
    await input.fill(text);await settle();
    return page.evaluate(label=>{const trace=window.chatComposerTrace,current=trace.validate();trace.samples.push({label,...current});return current;},label);
  };
  try{
    const empty=await edit('empty','');
    for(let cycle=0;cycle<2;cycle++){
      const short=await edit('single line','Synthetic draft');
      const medium=await edit('growing multiline','Synthetic draft line\n'.repeat(2));
      const tall=await edit('multiline','Synthetic draft line\n'.repeat(5));
      const capped=await edit('height cap','Synthetic draft line\n'.repeat(30));
      const overflow=await edit('overflowing cap','Synthetic draft line\n'.repeat(60));
      const restored=await edit('cleared','');
      if(tall.composer-empty.composer<40)throw Error('Multiline input did not grow');
      if(medium.composer<=empty.composer+10||medium.composer>=tall.composer-10)throw Error('Composer did not grow progressively before its height cap');
      if(Math.abs(capped.input-overflow.input)>1)throw Error('Textarea exceeded its height cap');
      for(const current of [short,medium,tall,capped,overflow,restored]){
        const growth=current.composer-empty.composer;
        if(Math.abs(current.viewport-empty.viewport+growth)>2)throw Error('Composer did not make room in history');
        if(Math.abs(current.last-empty.last+growth)>2)throw Error('Latest message did not move with the composer: '+JSON.stringify({empty,current}));
      }
      if(Math.abs(restored.composer-empty.composer)>1||Math.abs(restored.last-empty.last)>2)throw Error('Clearing the composer did not restore its height and latest position');
    }
    // Set up historical reading independently of input support (mobile WebKit
    // exposes no wheel API). The native setter represents the reader's scroll,
    // outside the instrumented production writers. Textarea edits stay trusted.
    await page.evaluate(()=>{
      window.chatComposerTrace.mode='transition';
      const history=document.querySelector('.history:not([data-preparation-surface])');history.dispatchEvent(new WheelEvent('wheel',{deltaY:-1500,bubbles:true}));
      Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop').set.call(history,history.scrollTop-1500);history.dispatchEvent(new Event('scroll'));
    });
    await page.waitForTimeout(300);await settle();
    await page.evaluate(()=>{const t=window.chatComposerTrace;t.anchor=t.geometry();if(t.anchor.end<100)throw Error('Wheel input did not leave Latest');t.mode='reading';t.validate();});
    for(const [label,text]of [['reading multiline','Historical draft line\n'.repeat(5)],['reading cap','Historical draft line\n'.repeat(40)],['reading cleared','']])await edit(label,text);
    await page.evaluate(()=>{window.chatComposerTrace.mode='transition';});
    await page.locator('.latest-button').click();await settle();
    await page.evaluate(()=>{window.chatComposerTrace.mode='latest';window.chatComposerTrace.validate();});
    await edit('Latest after historical edit','Latest draft line\n'.repeat(6));await edit('Latest cleared again','');
    const result=await page.evaluate(()=>{
      const t=window.chatComposerTrace;t.active=false;
      return {frames:t.frames,trustedInputs:t.inputs,writes:t.writes.length,foreignWriters:t.writes.filter(stack=>!stack.includes('scrollToFn')).length,errors:t.errors,samples:t.samples};
    });
    if(result.errors.length||result.foreignWriters||!result.trustedInputs||!result.writes)throw Error(JSON.stringify(result));
    return result;
  }finally{await page.evaluate(()=>{if(window.chatComposerTrace)window.chatComposerTrace.active=false;});}
}
