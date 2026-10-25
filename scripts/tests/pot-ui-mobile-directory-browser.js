// Production shell with synthetic catalogue/subscriptions; no backend or model calls.
async (page,{origin=new URL(page.url()).origin,browser=page.context().browser()}={})=>{
  const assert=(ok,message)=>{if(!ok)throw Error(message);},results=[];
  for(const width of [320,390]){
    const context=await browser.newContext({viewport:{width,height:844},isMobile:true,hasTouch:true,deviceScaleFactor:1,reducedMotion:'no-preference',serviceWorkers:'block',userAgent:'Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36'});
    const mobile=await context.newPage();
    try{
      const cdp=await context.newCDPSession(mobile),errors=[];
      mobile.on('pageerror',error=>errors.push(error.message));
      await mobile.route('**/directory-fixture*',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><link rel="stylesheet" href="/styles.css"><main id="app"></main></html>'}));
      await mobile.goto(origin+'/directory-fixture?conversation=alpha&locale=en&theme=light');
      await mobile.evaluate(async()=>{
        document.documentElement.dataset.platform='browser';localStorage.setItem('pot-system-notifications-browser-v1','false');
        const {mountApp}=await import('/components/ui.js');
        const f=window.directoryFixture={scope:{space:'local',owner:'alpha'},delay:0,long:false,frames:[],clicks:0,refreshes:0};let change;
        const snapshot=()=>({kind:'reset',runtime_id:'directory-fixture',boot_id:'boot',owner_id:JSON.stringify(['local',f.scope.owner]),conversation_id:f.scope.owner,cursor:'1',running:false,turn_id:null,replies:[],messages:Array.from({length:f.long?80:2},(_,index)=>({id:'message-'+index,sequence:String(index+1),role:index%2?'assistant':'user',...(index%2?{reply_to:'message-'+(index-1)}:{}),text:'Synthetic conversation '+index+'. '+'Directory navigation retains this conversation. '.repeat(8),status:'complete'})),history:{before:'1',after:f.long?'80':'2',has_more:false,has_newer:false}});
        const client={production:true,chatCacheName:'directory-fixture-'+crypto.randomUUID(),runtime:{runtime_id:'directory-fixture',boot_id:'boot'},scope:()=>({...f.scope}),setScope:value=>Object.assign(f.scope,value),changes:callback=>{change=callback;return()=>{};},subscribe(receive,status){const timer=setTimeout(()=>{const value=snapshot();status('connected');receive(value);f.deliveredCount=value.messages.length;f.ready=true;},0);return()=>clearTimeout(timer);},async request(path){
          const name=new URL(path,location.href).pathname;
          if(name==='/api/v1/spaces')return {active_space_id:'local',items:[{id:'local',name:'Fixture',control_id:'directory-fixture',connected:true}]};
          if(name==='/api/v1/assistants'){await new Promise(resolve=>setTimeout(resolve,f.delay));f.refreshes++;return {items:Array.from({length:24},(_,index)=>({id:index?'assistant-'+index:'alpha',name:'Fixture assistant '+index,phase:'ready',capabilities:{version:1,operations:['chat.read','chat.send']}}))};}
          return {items:[],pending:[],policy:{},cursor:0,has_more:false};
        },send:async()=>{throw Error('Fixture cannot send messages');}};
        const root=document.querySelector('#app');f.dispose=mountApp(root,client);f.refresh=()=>change(['assistants'],{space:'local'});
        root.addEventListener('click',()=>f.clicks++);
        f.measure=()=>{const directory=root.querySelector('.assistant-directory'),header=root.querySelector('.titlebar'),r=root.getBoundingClientRect(),d=directory.getBoundingClientRect(),s=getComputedStyle(root);return {edge:root.classList.contains('is-edge-back'),open:root.classList.contains('show-directory'),visible:getComputedStyle(directory).display!=='none',top:d.top,left:d.left,right:d.right,expectedTop:r.top+parseFloat(s.paddingTop)+header.getBoundingClientRect().height,expectedLeft:r.left+parseFloat(s.paddingLeft),expectedRight:r.right-parseFloat(s.paddingRight),scroll:root.querySelector('.assistant-list').scrollTop,search:root.querySelector('.assistant-search').value};};
        f.sample=()=>{f.frames=[];f.clicks=0;const tick=()=>{f.frames.push(f.measure());f.frame=requestAnimationFrame(tick);};f.frame=requestAnimationFrame(tick);};
        f.stop=()=>{cancelAnimationFrame(f.frame);return {frames:f.frames,clicks:f.clicks,final:f.measure()};};
      });
      await mobile.waitForFunction(()=>directoryFixture.ready);
      const geometry=(frames,label)=>{
        assert(frames.length>0,label+': measured visible frames');
        for(const frame of frames)assert(Math.abs(frame.top-frame.expectedTop)<1&&Math.abs(frame.left-frame.expectedLeft)<1&&Math.abs(frame.right-frame.expectedRight)<1,label+': directory matches header and shell bounds '+JSON.stringify(frame));
      };
      const touch=async(type,x,y=22)=>cdp.send('Input.dispatchTouchEvent',{type,touchPoints:type==='touchEnd'||type==='touchCancel'?[]:[{x,y}]});
      const chat=async()=>{
        // Reopen the same owner without scrolling its offscreen row into view.
        if(await mobile.locator('#app').evaluate(root=>root.classList.contains('show-directory')))await mobile.locator('.assistant-row.is-selected').evaluate(row=>row.click());
        await mobile.waitForFunction(()=>!document.querySelector('#app').classList.contains('show-directory')&&document.querySelector('.message'));
      };
      // Keep native click generation: a small touch drift can also start edge Back.
      for(const [long,focused,delay]of [[false,false,0],[true,true,350]]){
        await mobile.evaluate(({long,delay})=>{directoryFixture.long=long;directoryFixture.delay=delay;},{long,delay});
        await mobile.locator('.narrow-navigation').click();await chat();
        await mobile.waitForFunction(()=>directoryFixture.deliveredCount===(directoryFixture.long?80:2));
        for(const dx of long?[13,14]:[0,12,13,14]){
          await chat();await mobile.locator('.composer-input').fill('Retained draft');
          if(!focused)await mobile.locator('.composer-input').blur();
          assert(await mobile.locator('.composer-input').evaluate(input=>document.activeElement===input)===focused,'Composer focus matches the scenario');
          const refreshes=await mobile.evaluate(()=>{const f=directoryFixture;if(f.delay)f.refresh();f.sample();return f.refreshes;});
          await touch('touchStart',20);await mobile.evaluate(()=>new Promise(requestAnimationFrame));
          if(dx)await touch('touchMove',20+dx);
          await mobile.evaluate(()=>new Promise(requestAnimationFrame));await touch('touchEnd');
          await mobile.waitForFunction(()=>document.querySelector('#app').classList.contains('show-directory')&&!document.querySelector('#app').classList.contains('is-edge-back'));
          await mobile.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
          const data=await mobile.evaluate(()=>directoryFixture.stop()),label=`${width}px drift ${dx}, long=${long}, focused=${focused}, delay=${delay}`;
          assert(data.clicks===1,label+': browser generated one click');
          if(dx>12){const overlap=data.frames.filter(frame=>frame.edge&&frame.open);assert(overlap.length>0,label+': captured edge Back and directory during settling');geometry(overlap,label+' settling');}
          else assert(!data.frames.some(frame=>frame.edge),label+': tap did not start edge Back');
          geometry(data.frames.filter(frame=>frame.visible),label+' all visible frames');geometry([data.final],label+' final');
          if(delay)await mobile.waitForFunction(count=>directoryFixture.refreshes>count,refreshes);
          await mobile.locator('.assistant-search').fill('Fixture');await mobile.locator('.assistant-list').evaluate(list=>list.scrollTop=300);
          await chat();assert(await mobile.locator('.composer-input').inputValue()==='Retained draft',label+': draft retained');
        }
      }
      // Chromium can hand large native touches to browser navigation (pointercancel).
      // Use the existing shell's synthetic-pointer convention for full commit/cancel.
      const pointer=async(type,x)=>mobile.evaluate(({type,x})=>document.querySelector('#app').dispatchEvent(new PointerEvent(type,{pointerId:9,pointerType:'touch',isPrimary:true,bubbles:true,cancelable:true,clientX:x,clientY:250})),{type,x});
      for(const cancel of [true,false]){
        await chat();await mobile.evaluate(()=>directoryFixture.sample());
        await pointer('pointerdown',5);await pointer('pointermove',150);
        geometry([await mobile.evaluate(()=>directoryFixture.measure())],`${width}px pure swipe preview`);
        await pointer(cancel?'pointercancel':'pointerup',150);
        await mobile.waitForFunction(()=>!document.querySelector('#app').classList.contains('is-edge-back'));
        const data=await mobile.evaluate(()=>directoryFixture.stop());
        geometry(data.frames.filter(frame=>frame.visible),`${width}px swipe ${cancel?'cancel':'commit'}`);
        assert(data.final.open===!cancel,`${width}px swipe preserves commit/cancel navigation`);
        if(!cancel){assert(data.final.search==='Fixture'&&data.final.scroll===300,`${width}px directory search and scroll retained`);geometry([data.final],`${width}px committed directory`);}
      }
      // A native cancelled touch must also settle without opening the directory.
      await chat();await touch('touchStart',20);await touch('touchMove',34);await touch('touchCancel');
      await mobile.waitForFunction(()=>!document.querySelector('#app').classList.contains('is-edge-back'));
      assert(!await mobile.locator('#app').evaluate(root=>root.classList.contains('show-directory')),`${width}px native cancelled touch stays in Chat`);
      await mobile.setViewportSize({width:1200,height:844});
      const desktop=await mobile.locator('.assistant-directory').boundingBox();
      assert(desktop.x===56&&desktop.y===0&&desktop.width===236,'Wide directory keeps its grid placement');
      assert(!await mobile.locator('.narrow-navigation').isVisible(),'Wide Chat hides phone navigation');
      await mobile.evaluate(()=>directoryFixture.dispose());assert(errors.length===0,errors.join('\n'));results.push({width,touchDrifts:[0,12,13,14],shortAndLong:true,focusedAndDelayed:true,commitAndCancel:true,desktop:true});
    }catch(error){throw Error(`${width}px directory: ${error.message}; `+JSON.stringify(await mobile.evaluate(()=>({frames:directoryFixture.frames.slice(-6),final:directoryFixture.measure(),refreshes:directoryFixture.refreshes})).catch(()=>({}))),{cause:error});}finally{await context.close();}
  }
  return {passed:true,mobileDirectory:results,errors:[]};
}
