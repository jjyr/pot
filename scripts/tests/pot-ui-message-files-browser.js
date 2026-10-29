// Run against TestResourceBrowserFixture with POT_MESSAGE_FILE_FIXTURE=1 and
// POT_RESOURCE_BEFORE_ASSETS pointing at the unmodified baseline web/src tree.
async page=>{
  const origin=new URL(page.url()).origin,assert=(value,message)=>{if(!value)throw Error(message);},errors=[],external=[],downloads=[];
  page.on('pageerror',error=>errors.push(error.message));page.on('download',value=>downloads.push(value));
  await page.route('https://resource-isolation.invalid/**',route=>{external.push(route.request().url());return route.abort();});
  await page.goto(origin+'/?locale=en&theme=light');await page.waitForSelector('pot-message-file > pot-resource');
  await page.waitForFunction(()=>document.querySelectorAll('pot-message-file > pot-resource').length===9&&[...document.querySelectorAll('pot-resource')].every(node=>node.dataset.state==='ready'));
  assert(await page.locator('pot-resource[data-kind="image"]').count()===2,'Image and GIF previews');
  assert(await page.locator('pot-resource[data-kind="markdown"]').count()===1,'Markdown preview');
  assert(await page.locator('pot-resource[data-kind="pdf"]').count()===1,'PDF preview');
  assert(await page.locator('pot-resource[data-kind="file"]').count()===1,'Archives use download card');
  assert(await page.locator('audio,video').evaluateAll(nodes=>nodes.length===2&&nodes.every(n=>n.paused&&!n.autoplay&&n.preload==='metadata')),'Media never autoplay');
  assert(await page.locator('.resource-interactive').getAttribute('sandbox')==='allow-scripts','HTML opaque sandbox');
  const html=page.frameLocator('.resource-interactive');await html.locator('#confirm').click();assert(await html.locator('#saved').textContent()==='已采用','Bundled HTML interaction');
  assert(await page.locator('.resource-interaction-result').count()===1,'HTML result remains local');
  await html.locator('body').evaluate(()=>window['fetch']('https://resource-isolation.invalid/dynamic').catch(()=>{}));await page.getByText('Unsupported HTML dependency',{exact:true}).waitFor();
  await page.getByRole('link',{name:'报告源码',exact:true}).click();await page.locator('dialog mark').waitFor();
  assert((await page.locator('dialog mark').textContent()).startsWith('Readable'),'Inline :line opens source at the selected line');
  await page.keyboard.press('Escape');await page.waitForFunction(()=>!document.querySelector('.file-reference-dialog'));
  assert(await page.getByRole('link',{name:'报告源码',exact:true}).evaluate(n=>n===document.activeElement),'Dialog restores keyboard focus');
  console.log('viewers, inline source and focus passed');
  const ids=await page.locator('pot-message-file').evaluateAll(nodes=>nodes.map(n=>n.row.resource.id));await page.reload();await page.waitForFunction(()=>document.querySelectorAll('pot-message-file').length===11&&[...document.querySelectorAll('pot-message-file')].every(n=>n.row?.state==='ready'));
  assert(JSON.stringify(ids)===JSON.stringify(await page.locator('pot-message-file').evaluateAll(nodes=>nodes.map(n=>n.row.resource.id))),'History reuses saved resource identities');
  console.log('history identities passed');
  const stable=await page.evaluate(async()=>{
    const {createReplyBubble}=await import('/components/reply.mjs'),{createResourceView}=await import('/components/resource-view.mjs'),{translate}=await import('/locales/messages.mjs');
    const host=document.createElement('div');document.body.append(host);const reply=createReplyBubble(host),resource={id:'fixture',name:'fixture.txt',mime_type:'text/plain',size:3,entry:'fixture.txt',files:[{path:'fixture.txt',size:3,mime_type:'text/plain',digest:'x'}],download:{path:'fixture.txt'}};
    let reads=0,ready=false;const options={t:key=>translate('en',key),messageFiles:async()=>{reads++;if(reads===1)return [];return [{id:'fixture',href:'fixture.txt',state:ready?'ready':'pending',resource:ready?resource:undefined}]},retryMessageFile:async()=>{},acquireResource:()=>({read:async()=>new Blob(['one']),release(){}}),downloadResource:async()=>{}};
    const text='Retained **selection**.\n\n[file](fixture.txt)';reply.update({id:'fixture',text,phase:'streaming'},options);const before=host.querySelector('strong');reply.update({id:'fixture',text:text+'\n',phase:'streaming'},options);const noStreamingRead=reads===0;
    reply.update({id:'fixture',text,phase:'complete'},options);await new Promise(r=>setTimeout(r,0));const component=host.querySelector('pot-message-file');document.documentElement.lang='zh-CN';component.refresh();const silentPending=!component.status&&component.getAttribute('aria-busy')==='true';document.documentElement.lang='en';component.refresh();const range=document.createRange();range.selectNodeContents(before);getSelection().removeAllRanges();getSelection().addRange(range);ready=true;for(let i=0;i<30&&!component.preview;i++)await new Promise(r=>setTimeout(r,50));
    const htmlSource=createResourceView({resource:{...resource,name:'source.html',mime_type:'text/html',entry:'source.html',files:[{...resource.files[0],path:'source.html',mime_type:'text/html'}],download:{path:'source.html'}},mode:'preview'},{line:2,acquireResource:()=>({read:async()=>new Blob(['<div>first</div>\n<script>mustRemainLiteral()</script>']),release(){},renderURL(){throw Error('HTML line reference must not open a frame');}})});host.append(htmlSource);await new Promise(r=>setTimeout(r,0));const htmlLineInert=!htmlSource.querySelector('iframe')&&htmlSource.querySelector('mark')?.textContent==='<script>mustRemainLiteral()</script>';
    const result={htmlLineInert,silentPending,noStreamingRead,sameBlock:before===host.querySelector('strong'),sameRef:component===host.querySelector('pot-message-file'),selection:getSelection().toString(),ready:!!component.preview};host.remove();getSelection().removeAllRanges();return result;
  });assert(stable.htmlLineInert&&stable.silentPending&&stable.noStreamingRead&&stable.sameBlock&&stable.sameRef&&stable.selection==='selection'&&stable.ready,'Stream finalization and async resolution preserve DOM and selection');
  for(const locale of ['en','zh-CN']){await page.evaluate(locale=>{document.documentElement.lang=locale;window.dispatchEvent(new Event('pot:locale'));},locale);assert(!(await page.locator('pot-resource').first().textContent()).includes('resource.'),'Localized resource labels');}
  console.log('stream DOM and selection passed');
  const directory='output/pr-images/feature-message-file-previews';
  for(const scenario of [{name:'chat-desktop-light',width:1280,height:800,theme:'light',locale:'en'},{name:'chat-mobile-dark',width:390,height:844,theme:'dark',locale:'zh-CN'}]){
    await page.setViewportSize({width:scenario.width,height:scenario.height});
    console.log('capturing '+scenario.name);
    let original;
    for(const version of ['before','after']){
      await page.goto(`${origin}/${version==='before'?'before/':''}?locale=${scenario.locale}&theme=${scenario.theme}`);await page.locator('.assistant-row').first().waitFor();if(await page.locator('.assistant-row').first().isVisible())await page.locator('.assistant-row').first().click();await page.waitForSelector('.reply-content');
      if(version==='after')await page.waitForFunction(()=>document.querySelectorAll('pot-message-file > pot-resource').length===9&&[...document.querySelectorAll('pot-resource')].every(n=>n.dataset.state==='ready'));
      const dismiss=page.getByRole('button',{name:scenario.locale==='en'?'Not now':'暂时不用',exact:true});if(await dismiss.isVisible().catch(()=>false))await dismiss.click();
      await page.evaluate(()=>{document.querySelector('.history:not([data-preparation-surface])').scrollTop=0;});await page.waitForTimeout(150);
      const snapshot=await (await page.request.get(origin+'/api/v1/snapshot')).json(),text=JSON.stringify(snapshot.view.messages.map(({message:m})=>[m.id,m.text]));if(version==='before')original=text;else assert(original===text,'Before/after use identical original messages');
      assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'No horizontal page overflow');
      await page.screenshot({path:`${directory}/${scenario.name}-${version}.png`});
    }
  }
  await page.setViewportSize({width:320,height:740});await page.goto(origin+'/?locale=zh-CN&theme=light');await page.locator('.assistant-row').first().waitFor();if(await page.locator('.assistant-row').first().isVisible())await page.locator('.assistant-row').first().click();await page.waitForSelector('pot-message-file > pot-resource');assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'320px fits');
  assert(!external.length&&!downloads.length,'No remote images fetched or automatic downloads');assert(!errors.length,'No page errors: '+errors.join('; '));
  console.log('Message file product UI: MIME viewers, inline lines/focus, HTML isolation, history identities, stream/selection/DOM stability, en/zh-CN, desktop/390/320 and actual-source before/after passed');
}
