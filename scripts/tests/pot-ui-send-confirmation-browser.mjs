// CUA entry: fixtures/send-confirmation.html on a loopback static server.
// All transport, clocks and conversations are synthetic, using the real mountApp.
const params=new URLSearchParams(location.search);
const wait=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const until=async test=>{for(let i=0;i<150;i++){if(test())return;await wait(20);}throw Error('Fixture condition timed out');};
const check=(ok,message)=>{if(!ok)throw Error(message);};
if(params.has('frame'))await mount();else harness();
async function mount(){
  document.body.className='frame';
  if(params.has('mobile'))Object.defineProperty(navigator,'userAgent',{value:'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148'});
  const link=document.createElement('link');link.rel='stylesheet';link.href='/web/src/styles.css';document.head.append(link);await new Promise(resolve=>link.onload=resolve);
  const {mountApp}=await import('../../web/src/components/ui.js');
  const f=window.sendFixture={calls:[],queries:[],errors:[],receiptState:'unconfirmed',queryOffline:false,cursor:0,messages:[],running:true,scope:{space:'one',owner:'alpha'}};
  window.addEventListener('error',event=>f.errors.push(event.message));window.addEventListener('unhandledrejection',event=>f.errors.push(String(event.reason?.stack||event.reason)));
  const snap=()=>({runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify([f.scope.space,f.scope.owner]),conversation_id:'main',cursor:String(++f.cursor),replies:[],running:f.running,turn_id:f.running?'original':null,messages:f.messages,agent:{phase:'ready',attachments:{image:true,pdf:true}}});
  let receive;
  const client={production:true,runtime:{runtime_id:'fixture'},scope:()=>({...f.scope}),setScope:value=>Object.assign(f.scope,value),snapshot:async()=>snap(),
    subscribe:(onSnapshot,status)=>{receive=onSnapshot;queueMicrotask(()=>{status('connected');onSnapshot(snap());});return()=>{};},changes:()=>()=>{},
    request:async path=>{
      const name=new URL(path,location.href).pathname;
      if(name==='/api/v1/spaces')return {active_space_id:'one',items:[{id:'one',name:'Synthetic Space',control_id:'control',connected:true}]};
      if(name==='/api/v1/assistants')return [{id:'alpha',name:'Send confirmation fixture',revision:1}];
      if(name==='/api/v1/permissions')return {pending:[],policy:{machines:{}}};
      return {items:[],total:0,cursor:0,has_more:false};
    },
    send:(request,options)=>new Promise(resolve=>f.calls.push({request,options,resolve})),
    sendReceipt:async(request,options)=>{f.queries.push({request,options});if(f.queryOffline)throw Error('Fixture offline');return {...request,runtime_id:'control',owner_id:'alpha',state:f.receiptState,...(f.receiptState==='accepted'?{message_id:'accepted'}:{})};},
    cancel:async()=>{f.cancelled=true;},markRead:async()=>{},
  };
  f.publish=(confirm=false)=>{if(confirm){const r=f.calls.at(-1).request;f.messages.push({id:r.request_id,role:'user',text:r.text,status:'accepted',steering:true,accepted_request_id:r.request_id,accepted_turn_id:r.user_message_id});}receive(snap());};
  f.dispose=mountApp(document.querySelector('#app'),client);window.addEventListener('pagehide',f.dispose);
  await until(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);f.ready=true;
}
function harness(){
  const result=document.querySelector('#results'),frame=document.createElement('iframe');frame.title='Isolated send confirmation UI';document.querySelector('#app').append(frame);
  document.querySelector('#run').onclick=async()=>{
    result.textContent='Running…';const passed=[];
    try{for(const mobile of [false,true]){
      const loaded=new Promise(resolve=>frame.onload=resolve);frame.style.width=mobile?'390px':'1200px';frame.src=location.pathname+`?frame=1&assistant_id=alpha&conversation=main&locale=en&theme=light${mobile?'&mobile=1':''}`;
      await loaded;await until(()=>frame.contentWindow.sendFixture?.ready);
      const win=frame.contentWindow,doc=frame.contentDocument,f=win.sendFixture,input=doc.querySelector('#pot-composer'),send=doc.querySelector('.send-button');
      const type=text=>{input.value=text;input.dispatchEvent(new win.Event('input',{bubbles:true}));};
      const enter=extra=>input.dispatchEvent(new win.KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true,...extra}));
      type('First guidance');check(doc.querySelectorAll('.send-button').length===1,'single composer action');
      input.dispatchEvent(new win.CompositionEvent('compositionstart',{bubbles:true}));enter({isComposing:true});check(f.calls.length===0,'IME guard');input.dispatchEvent(new win.CompositionEvent('compositionend',{bubbles:true}));await wait(100);
      if(mobile){check(input.getAttribute('enterkeyhint')==='enter','mobile newline hint');enter();check(f.calls.length===0,'mobile Enter does not send');send.click();}else enter();
      await until(()=>f.calls.length===1);type('Next draft');check(send.disabled,'unknown send blocks next send');
      f.publish(false);check(send.disabled,'unrelated snapshot cannot confirm');
      f.publish(true);await until(()=>!send.disabled);check(input.value==='Next draft','new draft preserved');
      check(send.getAttribute('aria-label')==='Send guidance','streaming can accept next guidance');
      if(mobile)send.click();else enter();await until(()=>f.calls.length===2);
      f.calls[0].resolve({message_id:'old',duplicate:false});await wait(30);check(send.disabled,'late receipt cannot unlock next attempt');
      f.receiptState='accepted';await until(()=>input.value==='');check(send.getAttribute('aria-label')==='Stop','empty draft switches same button to Stop');
      passed.push(`${mobile?'phone':'desktop'}: IME, Enter/button, held HTTP + matching snapshot, next steer, late receipt, paged-out query, single button`);result.textContent=passed.join('\n');
      // Exhaust real bounded timeout/backoff once; no accelerated product timers.
      if(!mobile){f.receiptState='unconfirmed';f.queryOffline=true;type('Uncertain guidance');send.click();await until(()=>f.calls.length===3);
        await wait(23000);check([...doc.querySelectorAll('button')].some(node=>node.textContent==='Check and retry'&&node.getClientRects().length),'bounded unknown recovery is visible');
        win.dispatchEvent(new win.CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}}));
        await until(()=>doc.body.textContent.includes('查证并重试'));check(doc.body.textContent.includes('上一条消息可能已被接受'),'pending locale updates');
        f.queryOffline=false;f.receiptState='accepted';[...doc.querySelectorAll('button')].find(node=>node.textContent==='查证并重试').click();await until(()=>input.value==='');check(f.calls.length===3,'accepted recovery does not resend');
        passed.push('timeout: localized unknown state, bounded lookup, explicit accepted recovery without repost');result.textContent=passed.join('\n');
      }
      check(f.errors.length===0,f.errors.join('\n'));f.dispose();
    }result.textContent='PASS\n'+passed.join('\n');}catch(error){result.textContent='FAIL '+error.stack;}
  };
}
