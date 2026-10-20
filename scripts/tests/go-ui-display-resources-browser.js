// Run against TestResourceBrowserFixture with the cached Playwright CLI.
async page=>{
  const origin=new URL(page.url()).origin,assert=(value,message)=>{if(!value)throw Error(message);},errors=[],downloads=[],external=[];
  page.on('pageerror',error=>errors.push(error.message));page.on('download',download=>downloads.push(download));
  await page.route('https://resource-isolation.invalid/**',route=>{external.push(route.request().url());return route.abort();});
  const target=origin+'/scripts/tests/fixtures/display-resources.html',directory='output/pr-images';
  await page.setViewportSize({width:1200,height:1280});await page.goto(target);await page.waitForFunction(()=>window.fixtureReady);
  await page.waitForFunction(()=>[...document.querySelectorAll('pot-resource')].every(element=>element.dataset.state==='ready'));
  assert(await page.locator('pot-resource').count()===7,'All tool-created resource types are presented');assert(downloads.length===0,'Showing resources never downloads them');
  assert(await page.locator('audio').evaluate(media=>media.controls&&media.paused&&media.duration===2),'Audio loads metadata without autoplay');
  assert(await page.locator('video').evaluate(media=>media.controls&&media.paused&&media.duration>1),'Video loads metadata without autoplay');
  await page.locator('audio').click({position:{x:20,y:25}});
  await page.waitForFunction(()=>document.querySelector('audio').currentTime>0);
  await page.locator('audio').evaluate(media=>{media.pause();media.currentTime=1.2;});
  await page.waitForFunction(()=>document.querySelector('audio').currentTime>=1.19);
  await page.locator('video').click({position:{x:24,y:148}});await page.locator('video').evaluate(media=>media.play());
  await page.waitForFunction(()=>document.querySelector('video').currentTime>0);await page.locator('video').evaluate(media=>media.pause());
  const videoURL=await page.locator('video').getAttribute('src'),range=await page.request.get(new URL(videoURL,page.url()).href,{headers:{Range:'bytes=4-11'}});
  assert(range.status()===206&&(await range.body()).length===8,'Browser proxy preserves Range requests and partial responses');
  const html=page.frameLocator('iframe.resource-interactive');
  assert(await html.locator('body').getAttribute('data-isolated')==='yes','Sandbox prevents parent DOM access');
  const htmlCard=page.locator('pot-resource[data-kind="html"]'),baseHeight=await htmlCard.locator('iframe').evaluate(frame=>frame.getBoundingClientRect().height);
  await htmlCard.getByRole('button',{name:'展开',exact:true}).click();assert(await htmlCard.locator('iframe').evaluate(frame=>frame.getBoundingClientRect().height)>baseHeight,'Expand overrides the component-requested height');
  await htmlCard.getByRole('button',{name:'收起',exact:true}).click();assert(await htmlCard.locator('iframe').evaluate(frame=>frame.getBoundingClientRect().height)===baseHeight,'Collapse restores the component height without remounting');
  await html.getByRole('slider').fill('5000');assert(await html.locator('#total').textContent()==='¥60,000','Slider updates the actual HTML component');
  await html.getByRole('button',{name:'采用这个计划'}).click();
  await page.locator('.resource-interaction-result').filter({hasText:'60000'}).waitFor();
  const stable=await page.evaluate(()=>{const view=resourceFixture.views[5];window.retainedResource=view.host.querySelector('pot-resource');view.reply.update({...view.message,phase:'streaming'},resourceFixture.options);return retainedResource===view.host.querySelector('pot-resource');});
  assert(stable&&await html.locator('#budget').inputValue()==='5000','Reply refresh keeps the iframe and local state');
  await html.locator('body').evaluate(async()=>{
    try{await fetch('https://resource-isolation.invalid/fetch');}catch{}
    const image=document.createElement('img');image.src='https://resource-isolation.invalid/image';document.body.append(image);
    const frame=document.createElement('iframe');frame.src='https://resource-isolation.invalid/frame';document.body.append(frame);
    const form=document.createElement('form');form.action='https://resource-isolation.invalid/form';document.body.append(form);form.submit();
    window.location.href='https://resource-isolation.invalid/navigation';
  });
  assert(external.length===0,'Sandbox/CSP blocks external fetch, images, nested frames, forms and navigation');
  const imageID=await page.evaluate(()=>resourceFixture.messages[0].presentation.resource.id),failedDownload=`**/api/v1/resources/${imageID}/chunk?**`;
  await page.route(failedDownload,route=>route.fulfill({status:503,json:{code:'fixture_unavailable'}}));
  await page.getByRole('button',{name:'下载 landscape.png',exact:true}).click();
  await page.locator('pot-resource').first().getByText('文件下载失败，请重试。',{exact:true}).waitFor();
  assert(await page.locator('pot-resource').first().getAttribute('data-state')==='ready'&&downloads.length===0,'Download errors neither invalidate the preview nor save an error response');
  await page.evaluate(()=>{document.documentElement.lang='en';window.dispatchEvent(new Event('pot:locale'));});
  assert(await page.locator('pot-resource').first().getByText('Could not download this file. Try again.',{exact:true}).isVisible(),'Download failure labels switch language in place');
  await page.unroute(failedDownload);
  // Reload after the deliberately attempted navigation before capturing examples.
  await page.goto(target);await page.waitForFunction(()=>window.fixtureReady);await page.waitForFunction(()=>[...document.querySelectorAll('pot-resource')].every(element=>element.dataset.state==='ready'));
  await page.frameLocator('iframe.resource-interactive').getByRole('slider').fill('5000');
  await page.frameLocator('iframe.resource-interactive').getByRole('button',{name:'采用这个计划'}).click();
  await page.locator('.resource-interaction-result').waitFor();
  const saved=page.waitForEvent('download');await page.getByRole('button',{name:'下载 demo.zip',exact:true}).click();const zip=await saved;
  assert(zip.suggestedFilename()==='demo.zip','HTML originals download as a complete ZIP');
  await zip.saveAs(directory+'/resource-originals.zip');
  await page.locator('audio').evaluate(media=>{media.currentTime=0;});await page.locator('video').evaluate(async media=>{await media.play();});await page.waitForFunction(()=>document.querySelector('video').currentTime>.2);await page.locator('video').evaluate(media=>media.pause());
  await page.waitForFunction(()=>document.querySelector('video').readyState>=2&&!document.querySelector('video').seeking);
  await page.waitForTimeout(500); // Let native PDF/media compositors paint before capture.
  await page.screenshot({path:directory+'/resources-desktop-after.png',fullPage:true});
  await page.locator('pot-resource[data-kind="html"]').screenshot({path:directory+'/interactive-component-after.png'});
  await page.goto(target+'?before=1');await page.waitForFunction(()=>window.fixtureReady);await page.locator('pot-file-image[data-state="ready"]').last().waitFor();
  await page.screenshot({path:directory+'/resources-desktop-before.png',fullPage:true});
  await page.setViewportSize({width:390,height:2600});await page.goto(target+'?before=1');await page.waitForFunction(()=>window.fixtureReady);await page.locator('pot-file-image[data-state="ready"]').last().waitFor();
  await page.screenshot({path:directory+'/resources-mobile-before.png',fullPage:true});
  await page.goto(target);await page.waitForFunction(()=>window.fixtureReady);await page.waitForFunction(()=>[...document.querySelectorAll('pot-resource')].every(element=>element.dataset.state==='ready'));
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'All components fit the mobile viewport');
  await page.frameLocator('iframe.resource-interactive').getByRole('slider').fill('3500');assert(await page.frameLocator('iframe.resource-interactive').locator('#total').textContent()==='¥42,000','HTML interaction works at mobile width');
  await page.locator('video').evaluate(media=>media.play());await page.waitForFunction(()=>document.querySelector('video').currentTime>.2);await page.locator('video').evaluate(media=>media.pause());await page.waitForTimeout(500);
  await page.screenshot({path:directory+'/resources-mobile-after.png',fullPage:true});
  await page.locator('pot-resource[data-kind="html"]').screenshot({path:directory+'/interactive-mobile-after.png'});
  await page.evaluate(()=>{document.documentElement.lang='en';document.documentElement.dataset.theme='dark';window.dispatchEvent(new Event('pot:locale'));});
  assert(await page.getByRole('button',{name:'Download demo.zip',exact:true}).count()===1,'Mounted labels and accessibility text switch language');
  await page.setViewportSize({width:1200,height:1280});await page.screenshot({path:directory+'/resources-desktop-dark-after.png',fullPage:true});
  await page.emulateMedia({reducedMotion:'reduce'});await page.goto(target);await page.waitForFunction(()=>window.fixtureReady);
  await page.getByRole('button',{name:'播放动图',exact:true}).waitFor();assert(await page.locator('canvas.resource-gif-still').count()===1,'Reduced motion pauses GIFs');await page.emulateMedia({reducedMotion:'no-preference'});
  // Shared Task projection uses the same component and real API, independently
  // of the selected Main Chat's scope.
  await page.evaluate(async()=>{
    const {createTaskConversation}=await import('/web/src/components/task-conversation.mjs'),{createTaskTranscript,appendTaskEvents}=await import('/web/src/state/task-transcript.mjs');
    const f=resourceFixture,host=document.createElement('main');document.body.replaceChildren(host);
    const state=createTaskTranscript(),presentation=f.messages[5].presentation;
    appendTaskEvents(state,[{sequence:1,event:{turn_id:'task-run',update:{type:'tool_started',id:'show',name:'present_resource',args:{}}}},{sequence:2,event:{turn_id:'task-run',update:{type:'tool_finished',id:'show',result:{presentation}}}}]);
    f.task=createTaskConversation({root:host,task:{id:'fixture-task',phase:'complete'},record:{info:{phase:'complete'}},locale:()=>document.documentElement.lang,client:f.client,scope:{owner:'main',space:''},draft:'',onDraft:()=>{},toolMemory:new Map(),approvalFor:()=>null});f.task.update({state});
  });
  await page.locator('.task-transcript pot-resource[data-state="ready"]').waitFor();await page.frameLocator('iframe.resource-interactive').getByRole('slider').fill('4500');
  assert(await page.frameLocator('iframe.resource-interactive').locator('#total').textContent()==='¥54,000','Task conversations use the actual interactive renderer');
  await page.evaluate(()=>resourceFixture.task.dispose());
  // Actual product shell, not just the component showcase.
  await page.setViewportSize({width:1200,height:850});await page.goto(origin+'/?locale=zh-CN&theme=light');
  await page.locator('pot-resource').first().waitFor();const latest=page.getByRole('button',{name:'最新消息',exact:true});if(await latest.isVisible())await latest.click();await page.locator('pot-resource[data-kind="html"]').waitFor();
  const later=page.getByRole('button',{name:'暂时不用',exact:true});if(await later.isVisible())await later.click();
  await page.locator('pot-resource[data-kind="html"]').scrollIntoViewIfNeeded();await page.frameLocator('iframe.resource-interactive').getByRole('slider').fill('4000');
  assert(await page.frameLocator('iframe.resource-interactive').locator('#total').textContent()==='¥48,000','Production Main Chat mounts and operates the component');
  await page.locator('pot-resource[data-kind="html"]').evaluate(element=>{const history=element.closest('[role="log"]');if(history)history.scrollTop+=element.getBoundingClientRect().top-history.getBoundingClientRect().top-20;});
  await page.screenshot({path:directory+'/resources-chat-desktop-after.png'});
  assert(errors.length===0,errors.join('\n'));
  return {passed:true,resources:7,audio_playback:true,video_playback:true,range:true,html_interaction:true,isolation:true,download_zip:true,download_error_isolated:true,streaming_state:true,mobile:true,locale:true,reduced_motion:true,task:true,chat:true,errors};
}
