// Shared production client, Markdown, reply and tool components on an isolated origin.
async page=>{
  const assert=(value,message)=>{if(!value)throw Error(message);},errors=[],reads=[];
  page.on('pageerror',error=>errors.push(error.message));
  const task='21cfe7c9-34cc-41a8-9156-39a8341ad3dc';let png,hold=false,resume,started,fail=false;
  await page.route('**/api/v1/runtime',route=>route.fulfill({json:{runtime_id:'image-test'}}));
  await page.route('**/api/v1/tasks/*/file?*',async route=>{
    const url=new URL(route.request().url());reads.push(url);
    if(hold){started();await new Promise(resolve=>{resume=resolve;});}
    if(fail){await route.fulfill({status:403,json:{message:'Private file unavailable'}});return;}
    const offset=Number(url.searchParams.get('offset')),bytes=Buffer.from(png,'base64');
    await route.fulfill({json:{size:bytes.length,offset,revision:'fixture-image',data:bytes.subarray(offset,offset+192*1024).toString('base64')}});
  });
  try{
    await page.route('**/file-images-fixture?*',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><body></body></html>'}));
    await page.setViewportSize({width:1000,height:760});await page.goto(new URL('/file-images-fixture?locale=zh-CN&theme=light',page.url()).href);
    png=await page.evaluate(async task=>{
      const canvas=document.createElement('canvas');canvas.width=480;canvas.height=320;
      const c=canvas.getContext('2d');c.fillStyle='#deecf5';c.fillRect(0,0,480,320);c.fillStyle='#f3c35d';c.beginPath();c.arc(375,75,32,0,Math.PI*2);c.fill();
      c.fillStyle='#9ab9a9';c.beginPath();c.moveTo(0,320);c.lineTo(165,90);c.lineTo(350,320);c.fill();c.fillStyle='#4c7e71';c.beginPath();c.moveTo(180,320);c.lineTo(330,145);c.lineTo(480,320);c.fill();
      const {renderMarkdown}=await import('/components/markdown.mjs'),{createReplyBubble}=await import('/components/reply.mjs'),{createProductionClient}=await import('/adapters/production.mjs'),{translate}=await import('/locales/messages.mjs');
      const client=await createProductionClient();client.setScope({owner:'main',space:'local'});
      const host=document.createElement('main');host.style.cssText='width:780px;max-width:calc(100% - 48px);margin:48px auto;';document.body.replaceChildren(host);document.documentElement.lang='zh-CN';
      const row=document.createElement('article');row.className='message role-assistant';const bubble=document.createElement('div');bubble.className='message-body reply-bubble';row.append(bubble);host.append(row);
      const source=`可以，直接内嵌显示：\n\n![生成的风景](pot-file://${task}/landscape.png)`;
      const options={t:key=>translate(document.documentElement.lang,key),acquireFileImage:({task,path})=>client.acquireFileImage(task,path,{owner:'alpha',space:'remote'}),downloadFile:file=>{window.imageFixture.downloads.push(file);}};
      window.imageFixture={host,bubble,client,renderMarkdown,createReplyBubble,source,options,downloads:[],draw:(text=source)=>bubble.replaceChildren(renderMarkdown(text,options))};
      bubble.append(renderMarkdown(source));return canvas.toDataURL('image/png').split(',')[1];
    },task);
    await page.screenshot({path:'output/pr-images/generated-image-desktop-before.png'});
    await page.evaluate(()=>{const f=imageFixture;f.bubble.replaceChildren();f.reply=f.createReplyBubble(f.bubble);f.reply.update({text:f.source,phase:'complete'},f.options);f.client.setScope({owner:'beta',space:'local'});});
    await page.locator('pot-file-image[data-state="ready"]').waitFor();
    assert(reads.every(url=>url.searchParams.get('assistant_id')==='alpha'&&url.searchParams.get('space_id')==='remote'),'Image reads capture the original owner and Space');
    assert(await page.locator('pot-file-image img').evaluate(img=>img.naturalWidth===480),'Artifact bytes render as an inline image');
    await page.screenshot({path:'output/pr-images/generated-image-desktop-after.png'});
    await page.getByRole('button',{name:'放大图片',exact:true}).click();await page.locator('dialog[open] img').waitFor();
    assert(await page.locator('dialog img').evaluate(img=>img.src.startsWith('blob:')),'Enlarge uses the same bounded original');
    await page.evaluate(()=>{document.documentElement.lang='en';window.dispatchEvent(new Event('pot:locale'));});
    await page.getByRole('button',{name:'Download image',exact:true}).last().click();
    assert(await page.evaluate(()=>imageFixture.downloads[0].path)==='landscape.png','Download uses the original artifact path');
    await page.getByRole('button',{name:'Close preview',exact:true}).click();await page.locator('dialog').waitFor({state:'detached'});
    await page.setViewportSize({width:390,height:760});
    assert(await page.locator('pot-file-image img').evaluate(img=>img.getBoundingClientRect().right<=innerWidth),'Inline image fits mobile viewport');
    await page.screenshot({path:'output/pr-images/generated-image-mobile-after.png'});
    const count=reads.length;
    await page.evaluate(()=>{const f=imageFixture;f.reply.update({text:f.source+'\n\n已完成。',phase:'streaming'},f.options);});
    await page.locator('pot-file-image[data-state="ready"]').waitFor();assert(reads.length===count,'Streaming replacements reuse the image');
    await page.evaluate(task=>imageFixture.draw(`[风景](pot-file://${task}/landscape.png)\n\n![remote](https://blocked.example.test/pixel.png)\n\n![bad](pot-file://${task}/%2e%2e/private.png)\n\n[report](pot-file://${task}/report.txt)`),task);
    await page.locator('pot-file-image[data-state="ready"]').waitFor();assert(await page.locator('pot-file-image').count()===1,'Ordinary image links preview and invalid paths do not');assert(await page.locator('a[href="https://blocked.example.test/pixel.png"]').count()===1,'Remote images remain explicit links');assert(await page.locator('a').filter({hasText:'report'}).count()===1,'Nonimage files remain download links');
    await page.evaluate(()=>imageFixture.bubble.replaceChildren());fail=true;
    await page.evaluate(()=>{document.documentElement.lang='en';imageFixture.draw();});await page.locator('pot-file-image[data-state="error"]').waitFor();
    assert(await page.getByText('Could not preview this image. You can still download the original.',{exact:true}).count()===1,'Failures have localized fallback');
    await page.getByRole('button',{name:'Download image',exact:true}).click();
    fail=false;hold=true;const pending=new Promise(resolve=>{started=resolve;});
    await page.evaluate(()=>imageFixture.draw());await pending;
    await page.evaluate(()=>{document.documentElement.lang='zh-CN';window.dispatchEvent(new Event('pot:locale'));});
    assert(await page.getByText('正在加载图片……',{exact:true}).count()===1,'Language changes apply during IO');
    hold=false;resume();await page.locator('pot-file-image[data-state="ready"]').waitFor();
    await page.getByRole('button',{name:'放大图片',exact:true}).click();await page.evaluate(()=>imageFixture.bubble.replaceChildren());await page.locator('dialog').waitFor({state:'detached'});
    await page.evaluate(async task=>{
      const {createToolCall}=await import('/components/tool-call.mjs');const tool=createToolCall({...imageFixture.options});imageFixture.bubble.append(tool.element);
      tool.update({id:'tool',tool:{id:'tool',name:'get_messages',phase:'completed',args:{},result:{content:[{type:'text',text:`![风景](pot-file://${task}/landscape.png)`}]} }});
    },task);
    await page.locator('.tool-call > summary').click();
    await page.locator('pot-file-image[data-state="ready"]').waitFor({state:'attached'});
    const digest=await page.evaluate(async png=>[...new Uint8Array(await crypto.subtle.digest('SHA-256',Uint8Array.from(atob(png),char=>char.charCodeAt(0))))].map(byte=>byte.toString(16).padStart(2,'0')).join(''),png);
    await page.route('**/api/v1/message-files/**',route=>{const bytes=Buffer.from(png,'base64');reads.push(new URL(route.request().url()));return route.fulfill({json:[{id:'task-image-binding',state:'ready',href:`pot-file://${task}/landscape.png`,resource:{id:'task-image',name:'landscape.png',entry:'landscape.png',mime_type:'image/png',size:bytes.length,files:[{path:'landscape.png',size:bytes.length,mime_type:'image/png',digest}],download:{path:'landscape.png'}}}]});});
    await page.route('**/api/v1/resources/task-image/content?*',route=>{reads.push(new URL(route.request().url()));return route.fulfill({contentType:'image/png',body:Buffer.from(png,'base64')});});
    await page.route('**/api/v1/resources/task-image/chunk?*',route=>{reads.push(new URL(route.request().url()));const bytes=Buffer.from(png,'base64');return route.fulfill({json:{size:bytes.length,offset:0,revision:digest,data:png}});});
    await page.evaluate(async task=>{
      const f=imageFixture,{createTaskConversation}=await import('/components/task-conversation.mjs'),{createTaskTranscript}=await import('/state/task-transcript.mjs');
      f.bubble.replaceChildren();const root=document.createElement('div');f.host.replaceChildren(root);
      f.taskView=createTaskConversation({root,task:{id:task,phase:'complete'},record:{info:{phase:'complete'},result:{text:f.source}},locale:()=>document.documentElement.lang,client:f.client,scope:{owner:'alpha',space:'remote'},draft:'',onDraft:()=>{},downloadFile:f.options.downloadFile,toolMemory:new Map(),approvalFor:()=>null});
      f.taskView.update({state:createTaskTranscript()});
    },task);
    await page.locator('.task-transcript pot-resource[data-kind="image"][data-state="ready"]').waitFor({state:'attached'});
    assert(await page.locator('.task-transcript .resource-image').evaluate(img=>img.naturalWidth===480&&img.naturalHeight===320),'Task previews the original image bytes');
    assert(reads.filter(url=>url.pathname.includes('message-files')||url.pathname.includes('/resources/')).every(url=>url.searchParams.get('assistant_id')==='alpha'&&url.searchParams.get('space_id')==='remote'),'Task saved-image reads retain owner and Space');
    await page.evaluate(()=>{imageFixture.taskView.dispose();imageFixture.host.replaceChildren();});
    assert(errors.length===0,errors.join('\n'));
    return {passed:true,inline:true,download:true,dialog:true,owner_scope:true,streaming:true,mobile:true,locale:true,failure:true,task:true,tool:true,errors};
  }finally{resume?.();}
}
