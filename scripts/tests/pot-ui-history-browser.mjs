// Interactive fixture using production DOM, HTTP history and SSE with a local
// deterministic provider. Run through computer-use, without browser automation dependencies.
const frame=document.querySelector('iframe'),results=document.querySelector('#results');
// Background browser tabs can defer scroll/animation work past a timer. Sample
// only after the iframe has painted the requested projection.
const pause=ms=>new Promise(resolve=>setTimeout(()=>frame.contentWindow.requestAnimationFrame(()=>frame.contentWindow.requestAnimationFrame(resolve)),ms));
const wait=async(test,label)=>{for(let i=0;i<300;i++){if(test())return;await pause(20);}throw Error('Timed out: '+label);};
const check=(value,label)=>{if(!value)throw Error(label);results.textContent+='PASS '+label+'\n';};
function userScroll(history,top){const w=history.ownerDocument.defaultView;history.dispatchEvent(new w.WheelEvent('wheel',{deltaY:top-history.scrollTop||1,bubbles:true}));history.scrollTop=top;history.dispatchEvent(new w.Event('scroll'));}
const app=()=>frame.contentWindow,doc=()=>frame.contentDocument;
const anchor=()=>{const history=doc().querySelector('.history:not([data-preparation-surface])'),top=history.getBoundingClientRect().top;const row=[...doc().querySelectorAll('article.message')].find(row=>{const box=row.getBoundingClientRect();return box.height&&box.top<=top+4&&box.bottom>top;})||[...doc().querySelectorAll('article.message')].find(row=>{const box=row.getBoundingClientRect();return box.height&&box.top>top&&box.top<top+history.clientHeight;});return {id:row?.dataset.messageId,offset:row?.getBoundingClientRect().top-top};};
const position=id=>{const history=doc().querySelector('.history:not([data-preparation-surface])'),row=[...doc().querySelectorAll('article.message')].find(row=>row.dataset.messageId===id);return row?.getBoundingClientRect().top-history.getBoundingClientRect().top;};
async function run(mobile){
 results.textContent='Running '+(mobile?'mobile 390×700':'desktop 1100×650')+' checks…\n';
 try{
  frame.style.width=mobile?'390px':'1100px';frame.style.height=mobile?'700px':'650px';
  if(mobile){frame.src='chat-history-app.html?conversation=main&locale=zh-CN&theme=light';await new Promise(resolve=>frame.addEventListener('load',resolve,{once:true}));}
  await wait(()=>app().fixture&&doc().querySelector('article.message'),'mounted chat');await pause(700);
  const {client,metrics}=app().fixture,history=doc().querySelector('.history:not([data-preparation-surface])');
  check(metrics.frames[0].kind==='reset'&&metrics.frames[0].count<=113,'bounded initial snapshot: '+metrics.frames[0].count+' messages, '+metrics.frames[0].bytes+' bytes');
  check(doc().querySelectorAll('article.message').length<80,'initial mounted messages: '+doc().querySelectorAll('article.message').length);
  userScroll(history,history.scrollTop-24);await pause(200);
  check(history.scrollHeight-history.clientHeight-history.scrollTop>=23,'small upward scroll escapes bottom following');
  const smallScroll=anchor(),smallFrames=metrics.frames.length;
  await client.send({request_id:'small-scroll-'+Date.now(),conversation_id:'main',text:'Small-scroll reading fixture'});
  await wait(()=>metrics.frames.slice(smallFrames).some(value=>value.kind==='delta'),'update near bottom');await pause(800);
  check(Math.abs(position(smallScroll.id)-smallScroll.offset)<5,`streaming within 80px of bottom preserves paused reading (${smallScroll.id}: ${smallScroll.offset} → ${position(smallScroll.id)})`);
  doc().querySelector('.latest-button').click();await pause(200);
  userScroll(history,Math.max(300,history.scrollHeight/2));history.dispatchEvent(new Event('scroll'));await pause(300);
  const before=anchor(),pages=metrics.pages.length;doc().querySelector('.history-page-status').click();
  await wait(()=>metrics.pages.length>pages,'older page');await pause(800);
  const error=Math.abs(position(before.id)-before.offset);
  check(error<5,`prepend reading anchor ${before.id}: ${before.offset} → ${position(before.id)} (${error.toFixed(2)} px drift)`);
  // Delayed images and disclosure resizes above the reader retain their anchor.
  const beforeResize=anchor();const image=doc().querySelector('.sent-image img');if(image){image.style.height='310px';await pause(250);check(Math.abs(position(beforeResize.id)-beforeResize.offset)<5,'delayed image height preserves anchor');}
  const detail=doc().querySelector('details.tool-call');if(detail){detail.open=true;await pause(300);check(doc().querySelectorAll('article.message').length<90,'expanded tool sublist remains windowed');detail.open=false;}
  const input=doc().querySelector('.composer-input');input.value='browser-history-fixture';input.dispatchEvent(new Event('input',{bubbles:true}));
  const reading=anchor(),oldFrames=metrics.frames.length;doc().querySelector('form.composer').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));
  await wait(()=>metrics.frames.slice(oldFrames).some(value=>value.kind==='delta'),'real delta');await pause(1000);
  const deltas=metrics.frames.slice(oldFrames).filter(value=>value.kind==='delta');
  check(deltas.length>0&&deltas.every(value=>value.count<10),'real incremental frames: '+deltas.length+', max '+Math.max(...deltas.map(value=>value.bytes))+' bytes');
  check(Math.abs(position(reading.id)-reading.offset)<5,'streaming while reading does not scroll to bottom');
  app().fixture.failPage();doc().querySelector('.history-page-status').click();await pause(200);
  check(/Retry|重试/.test(doc().querySelector('.history-page-status').textContent),'history failure exposes retry');
  doc().querySelector('.history-page-status').click();await pause(500);
  check(!/Retry|重试/.test(doc().querySelector('.history-page-status').textContent),'history retry recovers');
  // A reference resolves directly by message ID rather than loading all pages.
  const reference=doc().querySelector('.message-references button');if(reference){const count=metrics.pages.length;reference.click();await wait(()=>metrics.pages.slice(count).some(page=>page.query.around==='m-0002'),'ID locate');await pause(500);check(!!doc().querySelector('[data-message-id="m-0002"]'),'reference target mounted by ID');}
  app().fixture.disconnect();app().dispatchEvent(new Event('online'));await pause(1000);
  check(metrics.frames.filter(value=>value.kind==='reset').length>=2,'disconnect recovers with bounded authoritative reset');
  const latest=doc().querySelector('.latest-button');latest.click();await pause(600);check(history.scrollHeight-history.clientHeight-history.scrollTop<90,'Latest returns to bottom');
  const labels=doc().querySelector('.history-page-status').textContent;
  check(mobile?/加载|开头/.test(labels):/Load|Beginning/.test(labels),'current locale history labels');
  const selection=app().getSelection(),prose=doc().querySelector('.reply-content');
  if(prose?.textContent){const range=doc().createRange();range.selectNodeContents(prose);selection.removeAllRanges();selection.addRange(range);const selectedRow=prose.closest('article');userScroll(history,Math.max(300,history.scrollTop-1500));history.dispatchEvent(new Event('scroll'));await pause(200);check(selectedRow.isConnected,'selected text remains mounted');selection.removeAllRanges();}
  doc().querySelector('.latest-button').click();await pause(400);
  // Failure restores the same original input, and retry must not duplicate it.
  const setInput=text=>{input.value=text;input.dispatchEvent(new Event('input',{bubbles:true}));};
  const submit=()=>doc().querySelector('form.composer').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));
  if(!mobile){await client.send({request_id:'failure-fixture-'+Date.now(),conversation_id:'main',text:'fail-once-fixture',references:[{message_id:'m-0002',quote:'Synthetic answer'}]});await wait(()=>doc().querySelector('.reply-bubble.is-failed'),'provider failure');await wait(()=>input.value==='fail-once-fixture','failed original draft restored');submit();await wait(()=>!doc().querySelector('.reply-bubble.is-failed')&&!input.value,'original retry');await pause(600);check([...doc().querySelectorAll('.message-prose')].filter(node=>node.textContent==='fail-once-fixture').length===1,'failed resend keeps one original input');}
  await wait(()=>!doc().querySelector('.reply-bubble.is-waiting,.reply-bubble.is-streaming'),'previous turn completed');
  setInput('steering-primary-fixture');submit();await wait(()=>doc().querySelector('.reply-bubble.is-waiting,.reply-bubble.is-streaming'),'active turn');setInput('steering-addition-fixture');await wait(()=>!doc().querySelector('.send-button').disabled,'steering receipt ready');submit();await pause(1000);const afterSteering=await client.snapshot();check(afterSteering.messages.some(message=>message.steering&&message.text==='steering-addition-fixture'),'steering input reconciles during streaming');
  let maxDOM=doc().querySelectorAll('article.message').length;
  for(let page=0;page<10;page++){
    userScroll(history,500);history.dispatchEvent(new Event('scroll'));await pause(80);
    const pagesBefore=metrics.pages.length;doc().querySelector('.history-page-status').click();await wait(()=>metrics.pages.length>pagesBefore,'long traversal page');await pause(100);
    maxDOM=Math.max(maxDOM,doc().querySelectorAll('article.message').length);
  }
  check(maxDOM<90,'ten-page traversal mounted at most '+maxDOM+' messages');
  check(metrics.errors.length===0,'no browser errors');
  check(doc().querySelectorAll('article.message').length<90,'final mounted messages: '+doc().querySelectorAll('article.message').length);
  results.textContent+='DONE '+(mobile?'mobile':'desktop')+' — '+metrics.pages.length+' page reads; largest page '+Math.max(...metrics.pages.map(page=>page.count))+' messages\n';
 }catch(reason){results.textContent+='FAIL '+reason.message+'\n'+reason.stack+'\n';}
}
document.querySelector('#run').onclick=()=>run(false);document.querySelector('#mobile').onclick=()=>run(true);
document.querySelector('#scroll').onclick=async()=>{
 results.textContent='Running upward-scroll boundary checks…\n';
 try{
  for(const mobile of [false,true]){
   frame.style.width=mobile?'390px':'1100px';frame.style.height=mobile?'700px':'650px';
   for(const gesture of ['wheel','touch','keyboard']){
    frame.src=`chat-history-app.html?conversation=main&locale=${mobile?'zh-CN':'en'}&theme=light&short=1&case=${gesture}`;
    await new Promise(resolve=>frame.addEventListener('load',resolve,{once:true}));
    await wait(()=>app().fixture&&doc().querySelector('article.message'),'short chat');await pause(500);
    const history=doc().querySelector('.history:not([data-preparation-surface])'),{metrics,client}=app().fixture;
    check(history.scrollHeight<=history.clientHeight+1,`${mobile?'mobile':'desktop'} ${gesture}: short window cannot scroll`);
    if(gesture==='wheel'){
     history.dispatchEvent(new WheelEvent('wheel',{deltaY:-1,ctrlKey:true,bubbles:true}));
     history.dispatchEvent(new WheelEvent('wheel',{deltaY:-1,deltaX:20,bubbles:true}));
     const pane=doc().createElement('div'),content=doc().createElement('div');pane.style.cssText='height:20px;overflow-y:auto';content.style.height='100px';pane.append(content);history.append(pane);pane.scrollTop=10;
     content.dispatchEvent(new WheelEvent('wheel',{deltaY:-1,bubbles:true}));await pause(100);
     check(metrics.pages.length===0,'zoom, horizontal gestures and nested output scrolling do not page chat');pane.remove();
    }
    const read=client.history;client.history=async(...args)=>{await pause(150);return read(...args);};
    const dispatchTouch=(type,y)=>{const event=new Event(type,{bubbles:true});Object.defineProperty(event,'touches',{value:[{clientY:y}]});history.dispatchEvent(event);};
    const gestureUp=()=>{
     if(gesture==='wheel')history.dispatchEvent(new WheelEvent('wheel',{deltaY:-1,bubbles:true}));
     else if(gesture==='touch'){dispatchTouch('touchstart',100);dispatchTouch('touchmove',101);}
     else history.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true}));
    };
    gestureUp();gestureUp();gestureUp();
    await wait(()=>metrics.pages.length>0,'gesture loads history without scroll event');await pause(300);
    check(metrics.pages.length===1,`${gesture}: repeated gestures share one pending history read`);
    check(history.scrollHeight>history.clientHeight,`${gesture}: older messages become scrollable`);
    const count=metrics.pages.length;userScroll(history,history.clientHeight-10);
    await wait(()=>metrics.pages.length>count,'one viewport prefetch');
    check(metrics.errors.length===0,'no browser errors');
   }
   frame.src=`chat-history-app.html?conversation=main&locale=${mobile?'zh-CN':'en'}&theme=light&short=complete`;
   await new Promise(resolve=>frame.addEventListener('load',resolve,{once:true}));
   await wait(()=>app().fixture&&doc().querySelector('article.message'),'complete short chat');await pause(500);
   const history=doc().querySelector('.history:not([data-preparation-surface])'),{metrics}=app().fixture;
   check(history.scrollHeight<=history.clientHeight+1,'all history fits within viewport');
   history.dispatchEvent(new WheelEvent('wheel',{deltaY:-24,bubbles:true}));
   history.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true}));await pause(200);
   check(metrics.pages.length===0&&history.scrollTop===0&&doc().querySelector('.latest-button').hidden,'complete short history stays still without paging or Latest');
   check(doc().querySelector('.history-page-status').disabled,'beginning of conversation has no more pages');
   const framesBefore=metrics.frames.length;
   await app().fixture.client.send({request_id:'short-follow-'+Date.now(),conversation_id:'main',text:'New message after scrolling a complete short conversation'});
   await wait(()=>metrics.frames.slice(framesBefore).some(value=>value.kind==='delta'),'short chat receives new messages');await pause(800);
   check(history.scrollHeight-history.clientHeight-history.scrollTop<=1&&doc().querySelector('.latest-button').hidden,'new messages still follow after gestures on complete short history');
  }
  results.textContent+='DONE upward-scroll boundary checks\n';
 }catch(reason){results.textContent+='FAIL '+reason.message+'\n'+reason.stack+'\n';}
};
document.querySelector('#earlier').onclick=()=>doc().querySelector('.history-page-status').click();document.querySelector('#latest').onclick=()=>doc().querySelector('.latest-button').click();

document.querySelector('#extras').onclick=async()=>{
 results.textContent='Running cache, viewport and live-locale checks…\n';
 try{
  await wait(()=>app().fixture&&doc().querySelector('article.message'),'mounted chat');
  const {client,metrics}=app().fixture,history=doc().querySelector('.history:not([data-preparation-surface])');
  userScroll(history,Math.max(500,history.scrollHeight/2));history.dispatchEvent(new Event('scroll'));await pause(500);
  const before=anchor();frame.style.width='700px';await pause(500);
  check(Math.abs(position(before.id)-before.offset)<5,'viewport resize preserves reading anchor');
  frame.style.width='390px';await pause(500);check(Math.abs(position(before.id)-before.offset)<5,'phone width restore preserves reading anchor');
  const originalHistory=client.history;client.history=async(...args)=>{await pause(250);return originalHistory(...args);};
  const pages=metrics.pages.length;doc().querySelector('.history-page-status').click();
  app().dispatchEvent(new (app().CustomEvent)('pot:preference',{detail:{key:'pot-ui-locale',value:'en'}}));
  await wait(()=>metrics.pages.length>pages,'page with locale change');await pause(200);
  check(/Load|Beginning/.test(doc().querySelector('.history-page-status').textContent),'pending history completion uses current English locale');
  app().dispatchEvent(new (app().CustomEvent)('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}}));await pause(100);
  check(/加载|开头/.test(doc().querySelector('.history-page-status').textContent),'mounted history labels switch to Chinese');
  client.history=originalHistory;
  const saved=anchor();
  await client.request('/api/v1/assistants',{method:'POST',body:{request_id:'cache-fixture-'+Date.now(),name:'Cache fixture other',text:''}});
  await wait(()=>[...doc().querySelectorAll('.assistant-row')].some(row=>row.textContent.includes('Cache fixture other')),'second assistant directory');
  [...doc().querySelectorAll('.assistant-row')].find(row=>row.textContent.includes('Cache fixture other')).click();await wait(()=>client.scope().owner!=='main','other assistant');
  [...doc().querySelectorAll('.assistant-row')].find(row=>row.textContent.includes('History fixture')).click();await wait(()=>client.scope().owner==='main','cached assistant return');
  await pause(800);check(Math.abs(position(saved.id)-saved.offset)<5,'cached navigation and authoritative refresh preserve anchor');
  check(doc().querySelector('.chat-approvals').isConnected&&!doc().querySelector('.message-window').contains(doc().querySelector('.chat-approvals')),'approval region remains outside recycled messages');
  check(metrics.errors.length===0,'no browser errors');results.textContent+='DONE cache and resize checks\n';
 }catch(reason){results.textContent+='FAIL '+reason.message+'\n'+reason.stack+'\n';}
};
