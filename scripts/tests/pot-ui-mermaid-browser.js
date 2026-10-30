// Shared production Markdown and reply components, exercised through Playwright
// CLI against the disposable preview or authenticated HTTP integration fixture.
async page=>{
  // Service-worker responses bypass Playwright routing. Isolate the module-load
  // tests while retaining the integration fixture's authenticated cookies.
  const context=page.context();
  const assert=(value,message)=>{if(!value)throw Error(message);},errors=[],remote=[];
  const onError=error=>errors.push(error.message);
  const onRequest=request=>{if(new URL(request.url()).hostname.endsWith('.test'))remote.push(request.url());};
  page.on('pageerror',onError);page.on('request',onRequest);
  await page.setViewportSize({width:1100,height:820});
  let release,started,loads=0;
  const pending=new Promise(resolve=>{release=resolve;}),requested=new Promise(resolve=>{started=resolve;});
  const vendorRoute=async route=>{loads++;started();await pending;await route.continue();};
  await page.route('**/vendor/pele.mjs',vendorRoute);
  try{
    await page.goto(new URL('/?locale=zh-CN&theme=light&conversation=preview-conversation',page.url()).href);
    await page.evaluate(async()=>{
      const {renderMarkdown}=await import('/components/markdown.mjs');
      const {createReplyBubble}=await import('/components/reply.mjs');
      const {translate}=await import('/locales/messages.mjs');
      const host=document.createElement('main');host.className='message-body reply-bubble';
      host.style.width='640px';host.style.maxWidth='100%';document.body.replaceChildren(host);
      document.documentElement.lang='zh-CN';document.documentElement.dataset.theme='light';
      const active=new Set(),Observer=window.ResizeObserver;
      window.ResizeObserver=class extends Observer{
        observe(element,...options){if(element.classList.contains('mermaid-canvas'))active.add(this);return super.observe(element,...options);}
        disconnect(){active.delete(this);return super.disconnect();}
      };
      window.mermaidFixture={host,active,renderMarkdown,createReplyBubble,t:key=>translate('zh-CN',key),
        draw:source=>host.replaceChildren(renderMarkdown(source)),
        fence:source=>'```mermaid\n'+source+'\n```'};
      mermaidFixture.draw('Normal **Markdown**\n\n```js\nconst x = "<script>";\n```');
    });
    assert(loads===0,'Ordinary Markdown does not load Pele');
    assert(await page.locator('strong').innerText()==='Markdown','Inline Markdown still renders');
    await page.evaluate(()=>mermaidFixture.draw('```mermaid\nflowchart LR\n A --> B'));
    assert(await page.locator('pot-mermaid[data-unfinished="true"][aria-busy="true"]').count()===1,'An open Mermaid fence creates an unfinished loading shell');
    assert(await page.locator('pot-mermaid svg').count()===0,'The unfinished Mermaid shell renders no svg');
    assert(!await page.locator('.mermaid-source').evaluate(node=>node.open),'The unfinished Mermaid shell has no visible source');
    assert(loads===0,'No Pele load is triggered by the unfinished Mermaid shell');
    await page.evaluate(()=>mermaidFixture.draw('````mermaid\nflowchart LR\n A --> B\n```\n````suffix'));
    assert(await page.locator('pot-mermaid[data-unfinished="true"][aria-busy="true"]').count()===1,'Short fences and fences with trailing text do not terminate a diagram');
    assert(await page.locator('pot-mermaid svg').count()===0,'The unfinished shell renders no svg for short fences or trailing text');
    assert(!await page.locator('.mermaid-source').evaluate(node=>node.open),'The unfinished shell has no visible source for short fences or trailing text');

    await page.evaluate(()=>{
      const f=mermaidFixture;f.draw(f.fence('flowchart LR\n A[旧消息] --> B'));
      f.stale=f.host.firstElementChild;f.host.replaceChildren();
    });
    let timer;
    try{await Promise.race([requested,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('Pele module request was not intercepted')),10000);})]);}
    finally{clearTimeout(timer);}
    await page.evaluate(()=>mermaidFixture.draw(mermaidFixture.fence('flowchart LR\n A[开始] --> B[完成]')));
    release();
    await page.locator('pot-mermaid[data-state="ready"] svg').waitFor();
    assert(await page.evaluate(()=>!mermaidFixture.stale.diagram&&mermaidFixture.active.size===1),'A late module load mounts only the connected message');
    assert(loads===1,'Concurrent diagrams share one lazy module load');
    assert(await page.locator('svg').textContent().then(text=>text.includes('开始')),'Chinese labels render');
    assert(!await page.locator('.mermaid-source').evaluate(node=>node.open),'Source collapses after a successful render');
    await page.locator('.mermaid-source > summary').click();

    const beforeTheme=await page.locator('svg text').first().evaluate(node=>getComputedStyle(node).fill);
    await page.evaluate(()=>document.documentElement.dataset.theme='dark');
    const afterTheme=await page.locator('svg text').first().evaluate(node=>getComputedStyle(node).fill);
    assert(beforeTheme!==afterTheme,'Diagram text follows the current semantic theme');
    await page.setViewportSize({width:390,height:820});
    await page.waitForFunction(()=>document.querySelector('svg').getBoundingClientRect().width<=innerWidth);
    assert(await page.locator('.mermaid-source').evaluate(node=>node.open),'Responsive redraw preserves an opened source disclosure');
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'A narrow diagram does not overflow the page');
    await page.evaluate(()=>mermaidFixture.host.replaceChildren());
    assert(await page.evaluate(()=>mermaidFixture.active.size===0),'Removing a message disconnects its Pele observers');

    await page.setViewportSize({width:1100,height:820});
    await page.evaluate(()=>{
      const f=mermaidFixture;
      f.samples=[
        'flowchart LR\n A --> B',
        'sequenceDiagram\n participant A as 用户\n participant B as Pot\n A->>B: 你好',
        'classDiagram\n class User {\n +String name\n }',
        'stateDiagram-v2\n [*] --> Idle\n Idle --> Done',
        'erDiagram\n USER ||--o{ MESSAGE : sends',
      ];
      f.draw(f.samples.map(f.fence).join('\n\n'));
    });
    await page.waitForFunction(()=>document.querySelectorAll('pot-mermaid[data-state="ready"]').length===5);
    assert(await page.evaluate(()=>{
      const ids=[...document.querySelectorAll('svg [id]')].map(node=>node.id);
      return new Set(ids).size===ids.length;
    }),'Multiple diagrams have unique SVG IDs');

    await page.evaluate(()=>{
      const f=mermaidFixture;
      f.host.style.width='260px';
      f.draw(f.fence('sequenceDiagram\n'+['A','B','C','D','E','F','G','H'].map(name=>'participant '+name).join('\n')+'\nA->>H: 宽图缩放'));
    });
    await page.locator('pot-mermaid[data-state="ready"]').waitFor();
    await page.getByRole('button',{name:'放大',exact:true}).click();
    assert(await page.evaluate(()=>{
      const zoom=document.querySelector('pot-mermaid').diagram.zoom;return zoom.scale>zoom.fit;
    }),'Localized zoom controls enlarge a wide diagram');
    await page.getByRole('button',{name:'重置缩放',exact:true}).click();
    assert(await page.evaluate(()=>{
      const zoom=document.querySelector('pot-mermaid').diagram.zoom;return zoom.scale===zoom.fit;
    }),'Reset zoom restores the fitted diagram');

    await page.evaluate(async()=>{
      const f=mermaidFixture,{createToolCall}=await import('/components/tool-call.mjs');
      f.host.style.width='640px';f.host.replaceChildren();f.tool=createToolCall({locale:()=> 'zh-CN'});
      f.tool.update({tool:{id:'diagram-tool',name:'lookup',phase:'completed',args:{},result:{content:[{type:'text',text:f.fence('flowchart TD\n A[工具结果] --> B')}]}}});
      f.host.append(f.tool.element);
    });
    await page.locator('.tool-call > summary').click();
    await page.locator('pot-mermaid[data-state="ready"] svg').waitFor();
    assert(await page.locator('pot-mermaid svg').textContent().then(text=>text.includes('工具结果')),'Collapsed Markdown tool results render when opened');

    await page.evaluate(()=>{
      const f=mermaidFixture;
      f.draw(f.fence('flowchart LR\n A["<img src=https://leak.test/label onerror=alert(1)>"] --> B\n click A "https://leak.test/nav"')+'\n\n'+
        f.fence('flowchart LR\n A@{ img: "https://leak.test/image", label: "image" }'));
    });
    await page.waitForFunction(()=>document.querySelectorAll('pot-mermaid[data-state="ready"]').length===2);
    assert(await page.locator('svg a,svg image,img,script[src*="leak.test"]').count()===0,'Model diagram links, images and HTML cannot create active resources');
    assert(remote.length===0,'Diagrams make no remote requests');
    await page.evaluate(()=>mermaidFixture.draw(mermaidFixture.fence('flowchart LR\n A["<img onerror=alert(1)>')));
    await page.locator('pot-mermaid[data-state="error"]').waitFor();
    assert(await page.locator('.mermaid-source').evaluate(node=>node.open),'Syntax errors preserve and expose the source');
    assert(await page.locator('img').count()===0,'Error messages stay text');
    await page.evaluate(()=>mermaidFixture.draw(mermaidFixture.fence('flowchart LR\n%% '+'x'.repeat(20001))));
    await page.locator('pot-mermaid[data-state="error"]').waitFor();
    assert(await page.locator('.mermaid-canvas').innerText().then(text=>/limit|20000/i.test(text)),'Oversized source fails locally within the configured limit');

    await page.evaluate(()=>{
      const f=mermaidFixture;f.host.replaceChildren();f.reply=f.createReplyBubble(f.host);
      f.reply.update({phase:'streaming',text:'```mermaid\nflowchart TD\n A --> B'},{t:f.t});
    });
    assert(await page.locator('pot-mermaid[data-unfinished="true"][aria-busy="true"]').count()===1,'Reply streaming keeps an unfinished diagram as an unfinished loading shell');
    assert(await page.locator('pot-mermaid svg').count()===0,'The streaming unfinished diagram renders no svg');
    assert(!await page.locator('.mermaid-source').evaluate(node=>node.open),'The streaming unfinished diagram has no visible source');
    await page.evaluate(()=>{
      const f=mermaidFixture;f.reply.update({phase:'streaming',text:f.fence('flowchart TD\n A --> B')},{t:f.t});
    });
    await page.locator('pot-mermaid[data-state="ready"]').waitFor();
    await page.evaluate(()=>{
      const f=mermaidFixture;f.old=f.host.querySelector('pot-mermaid');f.reply.update({phase:'complete',text:f.fence('flowchart TD\n A --> B')+'\n\n完成。'},{t:f.t});
    });
    await page.locator('pot-mermaid[data-state="ready"]').waitFor();
    assert(await page.evaluate(()=>mermaidFixture.old===mermaidFixture.host.querySelector('pot-mermaid')&&mermaidFixture.active.size===1),'Appending reply text retains the existing diagram mount');
    await page.evaluate(()=>{
      const f=mermaidFixture,root=f.host.querySelector('pot-mermaid');root.remove();f.host.querySelector('.reply-content').append(root);
    });
    await page.locator('pot-mermaid[data-state="ready"]').waitFor();
    assert(await page.evaluate(()=>mermaidFixture.active.size===1),'Reconnecting a diagram creates one live mount');
    await page.evaluate(()=>mermaidFixture.host.replaceChildren());
    assert(await page.evaluate(()=>mermaidFixture.active.size===0),'Final cleanup releases all diagram observers');

    const broken=await page.context().newPage();
    try{
      await broken.route('**/vendor/pele.mjs',route=>route.abort());
      await broken.goto(new URL('/?locale=en',page.url()).href);
      await broken.evaluate(async()=>{
        const {renderMarkdown}=await import('/components/markdown.mjs');
        document.documentElement.lang='en';document.body.replaceChildren(renderMarkdown('```mermaid\nflowchart TD\n A --> B\n```'));
      });
      await broken.locator('pot-mermaid[data-state="error"]').waitFor();
      assert(await broken.locator('.mermaid-source').evaluate(node=>node.open),'A failed library load retains readable source');
      assert(await broken.locator('.mermaid-canvas').innerText().then(text=>text.includes('Could not render')),'A library failure has a localized diagram error');
    }finally{await broken.close();}
    assert(errors.length===0,errors.join('\n'));
    return {passed:true,types:5,lazy:true,streaming:true,theme:true,responsive:true,zoom:true,tool_result:true,lifecycle:true,safe_resources:true,limits:true,load_failure:true,errors};
  }finally{
    release();await page.unroute('**/vendor/pele.mjs',vendorRoute);
    page.off('pageerror',onError);page.off('request',onRequest);

  }
}
