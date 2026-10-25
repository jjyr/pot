// Playwright run-code expression against web/src served on loopback. All API/SSE
// responses are fixtures: no product data, model calls or user preferences.
// Serve web/src with: python -m http.server 18729 --bind 127.0.0.1 --directory web/src
// Run this expression with the project's Playwright run-code workflow.
async (page, {base='http://127.0.0.1:18729',captureOnly=false,browser=page.context().browser()}={}) => {
  const assert=(value,message)=>{if(!value)throw Error(message);};
  const results=[];
  for(const mobile of [false,true]){
    const context=await browser.newContext({viewport:{width:mobile?393:1200,height:852},locale:'en',
      ...(mobile?{isMobile:true,hasTouch:true,userAgent:'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1'}:{hasTouch:true})});
    const tab=await context.newPage(),errors=[],sends=[];
    const capabilities={version:1,scope:'owner',operations:['chat.read','chat.send','chat.subscribe','chat.completions','chat.attachments','chat.steer','task.list','task.read','task.events','task.input','task.interrupt','task.outcomes','approvals.manage']};
    tab.on('pageerror',error=>errors.push(error.message));
    let running=false,rejectNext=false;
    const snapshot=()=>({cursor:running?2:1,boot_id:'fixture-boot',conversation_id:'alpha',messages:[],turn:running?{user_message_id:'active-turn'}:null,agent:{phase:'ready',attachments:{image:true,pdf:true}}});
    await context.addInitScript(()=>{
      const frame=value=>({kind:'reset',base_cursor:0,view:{state:{...value,messages:[]},replies:[],messages:value.messages.map((message,index)=>({message,sequence:String(index+1),version:value.cursor})),history:{has_more:false,has_newer:false}}});
      const sources=new Set();
      window.EventSource=class extends EventTarget{
        constructor(url){super();this.url=String(url);sources.add(this);queueMicrotask(async()=>{this.onopen?.({});if(this.url.includes('/events')){const target=new URL(this.url,location.href);target.pathname=target.pathname.replace(/\/events$/,'/snapshot');const snapshot=await(await fetch(target)).json();if(sources.has(this))this.dispatchEvent(new MessageEvent('conversation',{data:JSON.stringify(frame(snapshot))}));}});}
        close(){sources.delete(this);}
      };
      window.__pushEnterSnapshot=value=>{for(const source of sources)if(source.url.includes('/events'))source.dispatchEvent(new MessageEvent('conversation',{data:JSON.stringify(frame(value))}));};
    });
    await context.route('**/access/status',route=>route.fulfill({json:{authenticated:true}}));
    await context.route('**/api/v1/**',async route=>{
      const path=new URL(route.request().url()).pathname;
      if(route.request().method()==='POST'&&(path==='/api/v1/messages'||path==='/api/v1/turn/steer'||path.endsWith('/task-enter/messages'))){
        sends.push({path,body:route.request().postDataJSON()});
        if(rejectNext){rejectNext=false;await route.fulfill({status:400,json:{message:'Fixture explicit rejection'}});return;}
        await route.fulfill({json:{message_id:'accepted',duplicate:false}});return;
      }
      if(path==='/api/v1/attachments'&&route.request().method()==='POST'){
        const body=route.request().postDataJSON();await route.fulfill({json:{ready:true,attachment:{id:body.id,name:body.name,mime_type:body.mime_type,size:body.size}}});return;
      }
      const data=path==='/api/v1/runtime'?{runtime_id:'enter-fixture',boot_id:'fixture-boot'}:
        path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Local',kind:'local',connected:true}]}:
        path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1,capabilities}]:
        path==='/api/v1/assistants/bootstrap'?{assistant:null}:
        path==='/api/v1/snapshot'?snapshot():
        path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:
        path==='/api/v1/tasks/outcomes'?{items:[],cursor:0,has_more:false}:
        (path==='/api/v1/tasks/query'||path==='/api/v1/tasks/catalog')?{items:[{id:'task-enter',title:'Enter fixture task',assistant_id:'alpha',phase:'running'}],total:1}:
        path==='/api/v1/tasks/task-enter'?{run_id:'task-run',info:{phase:'running'},request:{text:'Fixture task goal'},config:{model:'fixture'},capabilities}:
        path==='/api/v1/tasks/task-enter/messages'?[]:
        path==='/api/v1/tasks/task-enter/events'?{events:[],cursor:0,has_more:false}:
        path==='/api/v1/completions'?{items:[{token:'/skill:review',label:'Review',detail:'Review changes'}]}:{items:[]};
      await route.fulfill({json:data});
    });
    try{
      await tab.goto(`${base}/?locale=en&theme=light&conversation=alpha`);
      const input=tab.locator('#pot-composer'),send=tab.locator('.composer-area > form .send-button');
      await input.waitFor();await tab.waitForFunction(()=>!document.querySelector('#pot-composer').disabled);
      const shot=async phase=>{await tab.screenshot({path:`output/pr-images/mobile-enter/${mobile?'mobile':'desktop'}-enter-${phase}.png`});};
      if(captureOnly){
        await input.fill(mobile?'First line':'Desktop send');await input.press('End');await input.press('Enter');
        await tab.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
        if(!mobile)await input.fill('Next desktop draft');
        await shot('before');results.push({mobile,captureOnly:true});continue;
      }
      assert(await input.getAttribute('enterkeyhint')===(mobile?'enter':null),'Only mobile changes the keyboard hint');
      await input.press('Enter');assert(sends.length===0,'Empty Enter cannot send');
      await input.fill('First line\nSecond line');
      await input.evaluate(node=>node.setSelectionRange(5,5));
      if(mobile){
        await input.evaluate(node=>{
          window.__enterBeforeInput=[];
          node.addEventListener('beforeinput',event=>window.__enterBeforeInput.push(event.inputType));
          for(const inputType of ['insertLineBreak','insertParagraph']){
            const event=new InputEvent('beforeinput',{inputType,bubbles:true,cancelable:true});node.dispatchEvent(event);
            if(event.defaultPrevented)throw Error('Native line break was swallowed');
          }
        });
        await input.press('Enter');
        assert(await tab.evaluate(()=>window.__enterBeforeInput.filter(type=>type==='insertLineBreak').length>=2),'Native Enter follows the beforeinput line-break path');
        assert(await input.inputValue()==='First\n line\nSecond line','Enter inserts at the caret inside multiline text');
        assert(await input.evaluate(node=>node.selectionStart===6&&node.selectionEnd===6),'Native caret advances after newline');
        for(const key of ['Shift+Enter','Control+Enter','Meta+Enter','Alt+Enter'])await input.press(key);
        assert(sends.length===0,'No modifier Enter sends on mobile');
        // Synthetic events validate application event ordering, not an OS IME.
        await input.fill('中文');
        await input.evaluate(node=>{
          node.setSelectionRange(1,1);window.__enterInput=node;
          node.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));
          for(const extra of [{isComposing:true},{keyCode:229}]){const key=new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true,...extra});node.dispatchEvent(key);if(key.defaultPrevented)throw Error('IME event was swallowed');}
        });
        await tab.evaluate(value=>window.__pushEnterSnapshot(value),snapshot());
        assert(await input.evaluate(node=>node===window.__enterInput&&node.value==='中文'&&node.selectionStart===1),'Snapshot preserves preedit node, text and caret');
        await input.evaluate(node=>{
          node.dispatchEvent(new CompositionEvent('compositionend',{data:'中',bubbles:true}));
          const key=new KeyboardEvent('keydown',{key:'Enter',keyCode:13,bubbles:true,cancelable:true});node.dispatchEvent(key);if(key.defaultPrevented)throw Error('Candidate confirmation was swallowed');
        });
        assert(sends.length===0,'IME candidate confirmation never sends');
        await tab.waitForTimeout(100);await input.press('Enter');
        assert(await input.inputValue()==='中\n文'&&sends.length===0,'Later ordinary Enter still inserts a newline');
        await input.fill('/skill:r');await tab.locator('.completion-option').first().waitFor();await input.press('Enter');
        assert(await input.inputValue()==='/skill:r\n'&&sends.length===0,'Open completion cannot consume mobile Enter');
        await input.fill('/skill:r');await tab.locator('.completion-option').first().click();
        assert(await input.inputValue()===''&&await tab.locator('.composer-selector').count()===1,'Completion remains selectable by button');await tab.getByRole('button',{name:'Remove Review',exact:true}).click();
        await input.fill('First line');await input.press('End');await input.press('Enter');await shot('after');
        const payload=await input.inputValue(),before=sends.length;
        await send.click();await tab.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
        assert(sends.length===before+1&&sends.at(-1).body.text===payload,'Mobile click sends exactly once, preserving newlines');
        await input.fill('Accessible\nbutton');await send.focus();await send.press('Enter');
        await tab.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
        assert(sends.at(-1).body.text==='Accessible\nbutton','Keyboard activation of the send button remains accessible');
        rejectNext=true;await input.fill('Retain\nfailed draft');await send.click();
        await tab.waitForFunction(()=>!document.querySelector('.composer-area > form .send-button').disabled);
        assert(await input.inputValue()==='Retain\nfailed draft','Network failure retains the multiline draft');
        const failed=sends.at(-1).body;await send.click();await tab.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
        assert(sends.at(-1).body.request_id===failed.request_id,'Retry retains request identity');
        await input.fill('');await tab.locator('.composer input[type=file]').setInputFiles({name:'fixture.pdf',mimeType:'application/pdf',buffer:Buffer.from('%PDF-1.4\nfixture')});
        await tab.waitForFunction(()=>!document.querySelector('.composer-area > form .send-button').disabled);
        const attachmentsBefore=sends.length;await input.focus();await input.press('Enter');
        assert(sends.length===attachmentsBefore,'Attachment-only Enter cannot send');
        await input.fill('');await send.click();await tab.waitForFunction(()=>!document.querySelector('.composer-attachments').textContent.trim());
        assert(sends.at(-1).body.text===''&&sends.at(-1).body.attachments.length===1,'Attachment-only button send remains supported');
        running=true;await tab.evaluate(value=>window.__pushEnterSnapshot(value),snapshot());
        await input.fill('Task\nguidance');const steerBefore=sends.length;await input.press('Enter');
        assert(sends.length===steerBefore,'Running assistant does not steer on mobile Enter');
        await send.click();await tab.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
        assert(sends.at(-1).path==='/api/v1/turn/steer'&&sends.at(-1).body.user_message_id==='active-turn','Button retains steering target');
        await input.fill('Blocked implicit submit');const implicitBefore=sends.length;
        await input.evaluate(node=>node.form.requestSubmit());assert(sends.length===implicitBefore,'Mobile form requestSubmit cannot send');
      }else{
        const before=sends.length;await input.press('Shift+Enter');assert(await input.inputValue()==='First\n line\nSecond line'&&sends.length===before,'Desktop Shift+Enter stays native');
        await input.fill('Desktop send');await input.press('Enter');await tab.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
        assert(sends.length===before+1&&sends.at(-1).body.text==='Desktop send','Desktop Enter sends once');
        await input.fill('Next desktop draft');await shot('after');
        await input.fill('/skill:r');await tab.locator('.completion-option').first().waitFor();const completionBefore=sends.length;await input.press('Enter');
        assert(await input.inputValue()===''&&await tab.locator('.composer-selector').count()===1&&sends.length===completionBefore,'Desktop Enter still selects completion');await tab.getByRole('button',{name:'Remove Review',exact:true}).click();
        await tab.setViewportSize({width:393,height:852});await input.fill('Narrow desktop');await input.press('Enter');
        await tab.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
        assert(sends.at(-1).body.text==='Narrow desktop','Touch desktop narrow viewport still sends with Enter');
        await tab.setViewportSize({width:1200,height:852});
        await input.fill('桌面');const imeBefore=sends.length;
        await input.evaluate(node=>{node.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));node.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',isComposing:true,keyCode:229,bubbles:true,cancelable:true}));node.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));node.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',keyCode:13,bubbles:true,cancelable:true}));});
        assert(sends.length===imeBefore&&await input.inputValue()==='桌面','Desktop IME confirmation remains guarded');
        await tab.waitForTimeout(100);await input.press('Enter');await tab.waitForFunction(()=>document.querySelector('#pot-composer').value==='');
        assert(sends.length===imeBefore+1,'Desktop ordinary Enter sends after IME guard expires');
      }
      // Both composer call sites must honor the same policy.
      if(mobile)await tab.setViewportSize({width:1200,height:852});
      await tab.getByRole('button',{name:'Tasks',exact:true}).first().click();await tab.getByRole('button',{name:'Enter fixture task',exact:true}).click();
      const taskInput=tab.locator('.task-composer-area textarea'),taskSend=tab.locator('.task-composer-area .send-button');await taskInput.waitFor();
      await taskInput.fill('Task line');const taskBefore=sends.length;await taskInput.press('Enter');
      if(mobile){
        assert(await taskInput.inputValue()==='Task line\n'&&sends.length===taskBefore,'Task mobile Enter is a newline even in a wide viewport');
        await taskInput.evaluate(node=>node.form.requestSubmit());assert(sends.length===taskBefore,'Task mobile implicit submission is blocked');
        await taskSend.click();
      }
      await tab.waitForFunction(()=>document.querySelector('.task-composer-area textarea').value==='');
      assert(sends.length===taskBefore+1&&sends.at(-1).body.text===(mobile?'Task line\n':'Task line'),'Task sends once through the expected action');
      assert(errors.length===0,errors.join('\n'));results.push({mobile,sends:sends.length,errors});
    }catch(reason){throw Error(JSON.stringify({mobile,error:reason.message,url:tab.url(),draft:await tab.locator('#pot-composer').inputValue().catch(()=>''),text:(await tab.locator('body').innerText()).slice(-1800),errors}));}finally{await context.close();}
  }
  return results;
}
