// Actual shared components, deterministic data, no model or product state.
async page=>{
  const origin=new URL(page.url()).origin,assert=(ok,message)=>{if(!ok)throw Error(message);};
  const context=await page.context().browser().newContext({serviceWorkers:'block'});page=await context.newPage();
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/fluid-fixture',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><body><main id="app"></main></body></html>'}));
  await page.goto(origin+'/fluid-fixture');await page.setViewportSize({width:1200,height:820});
  await page.evaluate(async()=>{
    const {createReplyBubble}=await import('/components/reply.mjs');
    const {renderMarkdown}=await import('/components/markdown.mjs');
    const f=window.fluidFixture={};
    const host=document.createElement('div');host.className='message-body';host.style.cssText='width:300px;position:fixed;left:-10000px';document.body.append(host);
    const view=createReplyBubble(host),options={t:key=>key};
    let text='Stable paragraph with **selected text**.\n\n```js\n'+'longLine(); '.repeat(100)+'\n```\n\nTail';
    view.update({text,phase:'streaming'},options);const paragraph=host.querySelector('p'),code=host.querySelector('pre');code.scrollLeft=180;
    const range=document.createRange();range.selectNodeContents(paragraph);getSelection().removeAllRanges();getSelection().addRange(range);const selected=getSelection().toString();
    text+=' extended';view.update({text,phase:'streaming'},options);
    f.markdown={sameParagraph:host.querySelector('p')===paragraph,sameCode:host.querySelector('pre')===code,scroll:code.scrollLeft,selection:getSelection().toString()===selected};
    getSelection().removeAllRanges();
    const samples=['Paragraph\n\n- item\n  - nested\n\n```js\na();\n```\n\n| A | B |\n| --- | --- |\n| C | D |','> Quote\n> **bold**\n\n## Header\n\n---\n\n![link](https://example.test/image.png)'];
    f.incremental=true;
    for(const source of samples){for(let end=1;end<=source.length;end++){view.update({text:source.slice(0,end),phase:'streaming'},options);const expected=document.createElement('div');expected.append(renderMarkdown(source.slice(0,end)));if(host.querySelector('.reply-content').innerHTML!==expected.innerHTML){f.incremental=false;throw Error('Incremental Markdown differs at '+JSON.stringify(source.slice(0,end)));}}}
    host.remove();
  });
  const markdown=await page.evaluate(()=>fluidFixture.markdown);assert(markdown.sameParagraph&&markdown.sameCode&&markdown.scroll===180&&markdown.selection,'Appended text preserves completed blocks, selection and code scrolling');
  assert(await page.evaluate(()=>fluidFixture.incremental),'Incomplete Markdown converges to the full renderer');
  await page.evaluate(async()=>{
    const {createTaskConversation}=await import('/components/task-conversation.mjs');
    const {createTaskTranscript}=await import('/state/task-transcript.mjs');
    const f=fluidFixture,root=document.querySelector('#app');root.className='page-content';root.style.cssText='height:760px;width:100%;max-width:none';
    f.state=createTaskTranscript();f.state.messages=Array.from({length:2000},(_,index)=>({id:'message-'+index,role:'assistant',sequence:index,text:'Task step '+index,phase:'complete'}));f.state.cursor=2000;
    f.record={info:{phase:'running'},request:{text:'Long deterministic task'}};
    f.ui=createTaskConversation({root,task:{id:'long-task',assistant_id:'alpha'},record:f.record,locale:()=> 'en',client:{request:async()=>({items:[]})},scope:{space:'local',owner:'alpha'},draft:'Retain this draft',onDraft:()=>{},toolMemory:new Map(),approvalFor:()=>null,downloadFile:()=>{},onDecision:()=>{}});
    f.ui.update({state:f.state});f.ui.start();
  });
  await page.waitForFunction(()=>[...document.querySelectorAll('.task-event')].some(node=>node.textContent.includes('Task step 1999')));
  assert(await page.locator('.task-event').count()<50,'Long task mounts a bounded viewport');
  await page.locator('.task-history').evaluate(node=>{node.scrollTop=0;node.dispatchEvent(new Event('scroll'));});
  await page.waitForFunction(()=>[...document.querySelectorAll('.task-event')].some(node=>node.textContent.includes('Task step 0')));
  const before=await page.locator('.task-history').evaluate(node=>node.scrollTop);
  await page.evaluate(()=>{fluidFixture.state.messages.push({id:'new',role:'assistant',sequence:2001,text:'New offscreen task output',phase:'streaming'});fluidFixture.ui.update({state:fluidFixture.state});});
  await page.waitForTimeout(100);assert(Math.abs(await page.locator('.task-history').evaluate(node=>node.scrollTop)-before)<2,'New output preserves the older reading position');
  assert(await page.getByLabel('Task guidance',{exact:true}).inputValue()==='Retain this draft','Virtualization retains guidance');
  await page.locator('.task-composer-area .latest-button').click();await page.getByText('New offscreen task output',{exact:true}).waitFor();
  await page.evaluate(()=>{fluidFixture.ui.dispose();document.querySelector('#app').replaceChildren();});
  await page.evaluate(async()=>{
    const {createPages}=await import('/components/pages.mjs');const f=fluidFixture;
    let release;f.slow=new Promise(resolve=>release=resolve);f.release=release;f.calls=[];
    const client={runtime:{boot_id:'boot'},spaces:[{id:'fast',control_id:'fast'},{id:'slow',control_id:'slow'}],assistants:[],scope:()=>({space:'fast',owner:'alpha'}),request:async(path,options)=>{
      if(!path.startsWith('/api/v1/tasks/catalog'))return {items:[]};f.calls.push({path,...options});if(options.space==='slow')await f.slow;
      return {items:Array.from({length:50},(_,index)=>({id:options.space+index,assistant_id:'alpha',title:'Task '+options.space+' '+index,phase:'running',updated_at_ms:1000-index})),total:5000,next_offset:50,counts:{active:5000}};
    }};
    const root=document.querySelector('#app');root.className='page-host';root.style.cssText='height:760px;width:100%';
    f.pages=createPages({client,host:root,approvalHost:document.createElement('div'),chatApprovalHost:document.createElement('div'),locale:()=> 'en',notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});f.loading=f.pages.show('tasks');
  });
  await page.getByRole('button',{name:'Task fast 0',exact:true}).waitFor();
  assert(await page.evaluate(()=>fluidFixture.calls.length===2),'Task first paint reads one prefix per Space');
  await page.evaluate(async()=>{fluidFixture.release();await fluidFixture.loading;});
  await page.getByText('10,000 items · Page 1',{exact:true}).waitFor();
  const search=page.getByRole('searchbox');await search.focus();
  await search.evaluate(node=>{fluidFixture.search=node;node.dispatchEvent(new CompositionEvent('compositionstart'));node.value='中';node.dispatchEvent(new InputEvent('input',{isComposing:true}));});
  const reads=await page.evaluate(()=>fluidFixture.calls.length);await page.waitForTimeout(220);assert(await page.evaluate(()=>fluidFixture.calls.length)===reads,'IME composition does not query partial candidates');
  await search.evaluate(node=>{node.value='中文';node.dispatchEvent(new CompositionEvent('compositionend'));node.dispatchEvent(new InputEvent('input'));});
  await page.waitForFunction(()=>fluidFixture.calls.some(call=>call.path.includes('q=%E4%B8%AD%E6%96%87')));
  assert(await search.evaluate(node=>node===fluidFixture.search&&document.activeElement===node),'Live search preserves its input and focus');
  await page.evaluate(async()=>{await fluidFixture.pages.leave();fluidFixture.pages.dispose();document.querySelector('#app').replaceChildren();});
  await page.goto(origin+'/?locale=zh-CN&theme=light&conversation=main');
  await page.evaluate(async()=>{
    const {mountApp}=await import('/components/ui.js');const f=window.fluidFixture||{};window.fluidFixture=f;
    const scope={space:'local',owner:'alpha'},snapshot={runtime_id:'fluid',boot_id:'boot',owner_id:JSON.stringify(['local','alpha']),conversation_id:'alpha',cursor:'1',running:false,turn_id:null,messages:[{id:'reply',role:'assistant',text:'Stable conversation',status:'complete'}]};
    const client={production:true,runtime:{runtime_id:'fluid',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>Object.assign(scope,value),snapshot:async()=>snapshot,subscribe:(receive,status)=>{queueMicrotask(()=>{status('connected');receive(snapshot);});return ()=>{};},changes:()=>()=>{},send:async()=>{},request:async path=>path==='/api/v1/spaces'?{active_space_id:'local',items:[{id:'local',name:'Local',control_id:'fluid'}]}:path==='/api/v1/assistants'?[{id:'alpha',name:'Pot',revision:1}]:path==='/api/v1/permissions'?{pending:[],policy:{machines:{}}}:{items:[]}};
    const root=document.createElement('main');document.body.replaceChildren(root);f.dispose=mountApp(root,client);
  });
  await page.setViewportSize({width:390,height:844});await page.getByText('Stable conversation',{exact:true}).waitFor();
  await page.locator('#pot-composer').fill('中文草稿');
  assert(await page.locator('.composer').evaluate(node=>node.getBoundingClientRect().height)<=80,'Phone composer stays compact with a single-line draft');
  const gesture=async(points,cancel=false)=>page.evaluate(({points,cancel})=>{const root=document.querySelector('.pot-app');for(let index=0;index<points.length;index++){const event=new PointerEvent(index===0?'pointerdown':index===points.length-1?(cancel?'pointercancel':'pointerup'):'pointermove',{bubbles:true,cancelable:true,pointerId:9,pointerType:'touch',clientX:points[index],clientY:200});Object.defineProperty(event,'timeStamp',{value:index*50});root.dispatchEvent(event);}}, {points,cancel});
  await gesture([1,55,55],true);await page.waitForTimeout(280);assert(!await page.locator('.pot-app').evaluate(node=>node.classList.contains('show-directory')),'Cancelled Back springs to the same conversation');
  await gesture([1,150,150]);await page.waitForTimeout(280);assert(await page.locator('.pot-app').evaluate(node=>node.classList.contains('show-directory')),'Committed edge Back returns to the directory');
  await gesture([1,65,65],true);await page.waitForTimeout(280);assert(!await page.locator('.mobile-menu').isVisible(),'Cancelled opening leaves navigation closed');
  await gesture([1,160,160]);await page.waitForTimeout(280);assert(await page.locator('.mobile-menu').isVisible(),'Committed opening reveals navigation');
  await page.emulateMedia({reducedMotion:'reduce'});await gesture([250,20,20]);await page.waitForTimeout(30);assert(!await page.locator('.mobile-menu').isVisible(),'Reduced Motion settles immediately');
  await page.locator('.assistant-row').first().click();assert(await page.locator('#pot-composer').inputValue()==='中文草稿','Gestures retain the conversation draft');
  await page.emulateMedia({reducedMotion:'no-preference'});await page.evaluate(()=>fluidFixture.dispose());
  assert(errors.length===0,errors.join('\n'));await context.close();return {passed:true,markdown,incremental:true,longTaskVirtualization:true,progressiveSpaces:true,imeSearch:true,compactComposer:true,gestures:true,errors};
}
