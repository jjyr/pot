// Run against TestResourceBrowserFixture with POT_MARKDOWN_RESOURCE_FIXTURE=1.
async page=>{
  const origin=new URL(page.url()).origin,target=origin+'/scripts/tests/fixtures/markdown-resources.html',assert=(value,message)=>{if(!value)throw Error(message);};
  const external=[],errors=[],downloads=[],requests=[];
  page.on('pageerror',error=>errors.push(error.message));page.on('download',download=>downloads.push(download));page.on('request',request=>requests.push(request.url()));
  await page.route('https://resource-isolation.invalid/**',route=>{external.push(route.request().url());return route.abort();});
  await page.setViewportSize({width:1100,height:900});await page.goto(target);await page.waitForFunction(()=>window.fixtureReady);
  await page.waitForFunction(()=>[...document.querySelectorAll('pot-resource')].every(element=>element.dataset.state==='ready'));
  const md=page.locator('[data-entry="review.md"] pot-resource'),txt=page.locator('[data-entry="plain.txt"] pot-resource'),raw=page.locator('[data-entry="raw.md"] pot-resource');
  assert(await md.getAttribute('data-kind')==='markdown','Old text/plain .md uses Markdown');
  assert(await page.locator('[data-entry="review.MARKDOWN"] pot-resource').getAttribute('data-kind')==='markdown','Case-insensitive .markdown entry renders');
  assert(await md.locator('h1').textContent()==='Resource preview · 季度回顾','Headings render with Unicode');
  assert(await md.locator('table tbody tr').count()===2&&await md.locator('blockquote').count()===1&&await md.locator('ul li').count()===2,'Tables, quotes and lists render');
  assert(await md.locator('pre code').textContent()==="const report = { format: 'Markdown', source: 'original bytes', unicode: '你好' };\nconsole.log(report);",'Fenced code preserves lines');
  assert(await md.locator('p code').textContent()==='inline code','Inline code renders');
  assert(await txt.getAttribute('data-kind')==='text'&&await txt.locator('h1,table,button[aria-pressed]').count()===0,'Ordinary txt stays plain without a mode switch');
  assert(await txt.locator('pre').textContent()===await md.locator('.resource-text').textContent(),'Txt shows the same original text literally');
  assert(await raw.getAttribute('data-kind')==='markdown'&&await raw.locator('iframe,img,script').count()===0,'HTML-sniffed .md uses inert Markdown');
  assert(await md.locator('script,img,iframe,[onerror],a[href^="javascript:"]').count()===0,'Raw HTML, events and dangerous links are never active');
  assert(await md.locator('a[href="https://resource-isolation.invalid/image"]').count()===1,'Remote image is only an explicit link');
  assert((await md.locator('.resource-markdown').textContent()).includes('![Missing asset](missing.png)')&&(await md.locator('.resource-markdown').textContent()).includes('[Relative document](../private.txt)'),'Missing local assets and relative links remain literal');
  assert(await page.evaluate(()=>!window.markdownExecuted)&&external.length===0&&downloads.length===0,'Preview causes no script, external request or download');
  const original=await page.evaluate(async()=>{
    const resource=markdownFixture.views[0].message.presentation.resource;
    return await fetch(`/api/v1/resources/${resource.id}/content`).then(response=>response.text());
  });
  assert(original.includes('\r\n')&&original.includes('你好'),'Transport retains CRLF and Unicode');
  await md.getByRole('button',{name:'Show source',exact:true}).click();
  assert(await md.locator('.resource-text').isVisible()&&!await md.locator('.resource-markdown').isVisible(),'Switch shows source');
  assert(await md.locator('.resource-text').textContent()===original,'Source selection can copy exact loaded text');
  assert(await md.locator('.resource-text').evaluate(pre=>{const range=document.createRange();range.selectNodeContents(pre);getSelection().removeAllRanges();getSelection().addRange(range);const text=getSelection().toString();getSelection().removeAllRanges();return text.startsWith('# Resource preview')&&text.includes('<script>');}),'Source remains selectable for native text copy');
  await page.context().grantPermissions(['clipboard-read','clipboard-write'],{origin});
  await md.locator('.resource-text').evaluate(pre=>{const range=document.createRange();range.selectNodeContents(pre);getSelection().removeAllRanges();getSelection().addRange(range);});
  await page.keyboard.press('Control+c');
  // Chromium's native selection omits the terminal line break; the source DOM
  // and downloadable bytes above retain it.
  const copied=await page.evaluate(()=>navigator.clipboard.readText());assert(copied.replace(/\r\n/g,'\n')===original.replace(/\r\n/g,'\n').replace(/\n$/,''),'Native copy includes the complete source content');
  await page.evaluate(()=>{document.documentElement.lang='zh-CN';window.dispatchEvent(new Event('pot:locale'));});
  assert(await md.getByRole('button',{name:'显示 Markdown',exact:true}).count()===1,'Source mode labels update live in Chinese');
  await md.getByRole('button',{name:'显示 Markdown',exact:true}).click();await md.getByRole('button',{name:'显示源码',exact:true}).click();
  await md.getByRole('button',{name:'展开',exact:true}).click();
  assert(await md.getAttribute('class')==='is-expanded'&&await md.locator('.resource-text').isVisible(),'Expansion preserves source mode');
  await page.evaluate(()=>{const view=markdownFixture.views[0];window.retainedMarkdown=view.host.querySelector('pot-resource');view.reply.update({...view.message,phase:'streaming'},markdownFixture.options);});
  assert(await page.evaluate(()=>retainedMarkdown===markdownFixture.views[0].host.querySelector('pot-resource'))&&await md.locator('.resource-text').isVisible(),'Reply refresh preserves the current mode');
  await page.evaluate(()=>{const resource=document.querySelector('pot-resource'),parent=resource.parentNode;resource.remove();parent.append(resource);});
  await md.getByRole('button',{name:'显示源码',exact:true}).waitFor();await md.locator('h1').waitFor();
  assert(await md.locator('.resource-markdown').isVisible()&&!await md.locator('.resource-text').isVisible(),'Reopening defaults to Markdown');
  await md.getByRole('button',{name:'显示源码',exact:true}).click();
  const downloaded=page.waitForEvent('download');await md.getByRole('button',{name:'下载 review.md',exact:true}).click();
  const download=await downloaded;assert(download.suggestedFilename()==='review.md','Label cannot rename the original download');
  // Compare the actual browser download, not a reserialized Markdown string.
  const stream=await download.createReadStream(),chunks=[];for await(const chunk of stream)chunks.push(chunk);
  assert(Buffer.concat(chunks).equals(Buffer.from(original)),'Downloaded bytes are exactly the original');
  const variants=await page.evaluate(async()=>{
    const {resourcePreviewKind,createResourceView}=await import('/web/src/components/resource-view.mjs'),view=markdownFixture.views[0],r=view.message.presentation.resource;
    const checks=[
      [{...r,mime_type:'text/markdown; charset=utf-8',entry:'opaque'},'markdown'],
      [{...r,mime_type:'text/x-markdown',entry:'opaque'},'markdown'],
      [{...r,mime_type:'application/pdf',entry:'misleading.md'},'pdf'],
      [{...r,mime_type:'text/plain',entry:'ordinary.txt',name:'spoof.md'},'text'],
      [{...r,mime_type:'text/html',entry:'actual.html',name:'spoof.md'},'html'],
    ];
    if(!checks.every(([resource,kind])=>resourcePreviewKind(resource)===kind))return false;
    const explicit=createResourceView({...view.message.presentation,resource:{...r,mime_type:'text/markdown; charset=utf-8',entry:r.entry}},{...markdownFixture.options});explicit.id='explicit-markdown';document.querySelector('main').append(explicit);return true;
  });assert(variants,'MIME precedence and display-label independence');
  await page.locator('#explicit-markdown[data-state="ready"] h1').waitFor();
  // Production reader rejects oversized previews before issuing a chunk read.
  await page.evaluate(async()=>{
    const {createResourceView}=await import('/web/src/components/resource-view.mjs'),view=markdownFixture.views[0],r=view.message.presentation.resource;
    const resource={...r,id:r.id+'-large',size:17*1024*1024,files:r.files.map(file=>({...file,size:17*1024*1024}))};
    const card=createResourceView({...view.message.presentation,resource},markdownFixture.options);card.id='oversized-markdown';document.querySelector('main').append(card);
  });await page.locator('#oversized-markdown[data-state="error"]').waitFor();
  assert(await page.locator('#oversized-markdown').getByRole('button',{name:'下载 review.md',exact:true}).isVisible(),'Preview limit failure retains download');
  await page.goto(target);await page.waitForFunction(()=>window.fixtureReady);await md.locator('h1').waitFor();
  const id=await md.evaluate(element=>element.presentation.resource.id),failed=`**/api/v1/resources/${id}/chunk?**`;
  await page.route(failed,route=>route.fulfill({status:503,json:{code:'fixture_unavailable'}}));
  await page.reload();await page.waitForFunction(()=>window.fixtureReady);await page.locator('[data-entry="review.md"] pot-resource[data-state="error"]').waitFor();
  assert(await md.getByRole('button',{name:'Download review.md',exact:true}).isVisible(),'Load errors retain original download');
  await page.unroute(failed);await page.reload();await md.locator('h1').waitFor();
  await page.emulateMedia({colorScheme:'dark'});await page.evaluate(()=>{document.documentElement.dataset.theme='system';});
  const dark=await md.locator('.resource-markdown').evaluate(element=>getComputedStyle(element).color);
  await page.emulateMedia({colorScheme:'light'});
  assert(dark!==await md.locator('.resource-markdown').evaluate(element=>getComputedStyle(element).color),'Reader follows system appearance live');
  // Capture the real old and new modules against the identical registered file.
  const directory='output/pr-images/feat-markdown-resource-preview';
  for(const scenario of [{name:'markdown-desktop',width:1100,height:900,theme:'light',locale:'en'},{name:'markdown-mobile-dark',width:390,height:844,theme:'dark',locale:'zh-CN'}]){
    await page.setViewportSize({width:scenario.width,height:scenario.height});
    for(const version of ['before','after']){
      await page.goto(`${target}?capture=1&theme=${scenario.theme}&locale=${scenario.locale}${version==='before'?'&before=1':''}`);
      await page.waitForFunction(()=>window.fixtureReady&&document.querySelector('pot-resource')?.dataset.state==='ready');
      if(version==='after')await page.locator('pot-resource h1').waitFor();
      assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Reader fits mobile and desktop width');
      await page.screenshot({path:`${directory}/${scenario.name}-${version}.png`,fullPage:true});
      if(version==='after'){
        await page.getByRole('button',{name:scenario.locale==='en'?'Show source':'显示源码',exact:true}).click();
        await page.screenshot({path:scenario.locale==='en'?`${directory}/markdown-source-desktop-after.png`:`output/pr-images/${scenario.name}-source.png`,fullPage:true});
        await page.getByRole('button',{name:scenario.locale==='en'?'Show Markdown':'显示 Markdown',exact:true}).click();
      }
    }
  }
  assert(!requests.some(url=>/missing\.png|private\.txt/.test(url)),'Markdown cannot read undeclared assets or workspace paths');
  assert(external.length===0&&errors.length===0,'No external image requests or browser exceptions: '+errors.join('; '));
  console.log('Markdown resource browser checks passed; captured desktop/light and mobile/dark before/after.');
}
