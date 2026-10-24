// Playwright CLI expression. Real shared DOM; synthetic IO, no product data.
async page => {
  const {origin}=STATE, checks=[];
  const check=(value,message)=>{if(!value)throw Error(message);checks.push(message);};
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.setViewportSize({width:1200,height:820});
  await page.route('**/shell-fixture',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html lang="en" data-platform="browser"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="./styles.css"><div id="app"></div></html>'}));
  await page.goto(origin+'/shell-fixture');
  await page.evaluate(async()=>{
    const {mountApp}=await import('./components/ui.js');
    const fixture=window.shellFixture={scope:{space:'local',owner:'alpha'},reads:0,sends:0};
    const space={id:'local',control_id:'shell-control',name:'Local Space',kind:'local',connected:true};
    const assistants=Array.from({length:18},(_,i)=>({id:i?'assistant-'+i:'alpha',name:i?'Assistant '+i:'Alpha',revision:1}));
    const snapshot=()=>({runtime_id:'shell-control',boot_id:'boot',owner_id:JSON.stringify(['local',fixture.scope.owner]),conversation_id:fixture.scope.owner,cursor:'1',running:false,turn_id:null,messages:Array.from({length:32},(_,i)=>({id:'message-'+i,sequence:String(i+1),version:1,role:i%2?'assistant':'user',text:i%2?'A detailed reply with enough content to wrap as the conversation width changes. '.repeat(5):'Please help with task '+i,status:'complete'}))});
    const client={production:true,runtime:{runtime_id:'shell-control',boot_id:'boot'},spaces:[space],scope:()=>({...fixture.scope}),setScope:value=>fixture.scope={...value},
      async request(path){
        fixture.reads++;const name=new URL(path,location.href).pathname;
        if(name==='/api/v1/spaces')return {items:[space],active_space_id:'local'};
        if(name==='/api/v1/assistants')return assistants;
        if(name==='/api/v1/permissions')return {pending:[],policy:{},machines:[]};
        if(name==='/api/v1/providers')return {profiles:[{id:'p',name:'Provider',model:'fixture-model',revision:1}],default_provider_id:'p',directory:[],connections:[]};
        if(name==='/api/v1/agent')return {generation:1,revision:1,selection:{profile_id:'p',model:'fixture-model'}};
        if(name==='/api/v1/instructions')return {text:'',revision:1};
        return {items:[]};
      },snapshot:async()=>snapshot(),subscribe(receive,status){const timer=setTimeout(()=>{status('connected');receive(snapshot());},0);return ()=>clearTimeout(timer);},changes:()=>()=>{},send:async()=>{fixture.sends++;},downloadFile:()=>{}};
    fixture.dispose=mountApp(document.querySelector('#app'),client);
  });
  await page.waitForFunction(()=>document.querySelector('#pot-composer')&&!document.querySelector('#pot-composer').disabled);
  const input=page.locator('#pot-composer'),pane=page.locator('.assistant-directory');
  const settle=()=>page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  await settle();
  const geometry=await page.evaluate(()=>{
    const rect=selector=>document.querySelector(selector).getBoundingClientRect().toJSON();
    return {rail:rect('.feature-rail'),pane:rect('.assistant-directory'),header:rect('.titlebar')};
  });
  check(geometry.rail.y===0&&geometry.pane.y===0&&geometry.header.y===0,'All three columns start at the top');
  check(geometry.rail.height===820&&geometry.pane.height===820&&geometry.header.height===66,'Full-height columns and independent conversation header');
  await input.fill('Keep this draft 中文');
  await page.getByRole('searchbox',{name:'Search assistants'}).fill('Assistant');
  await page.evaluate(()=>{
    const history=document.querySelector('.history');history.scrollTop=600;history.dispatchEvent(new Event('scroll'));
    const list=document.querySelector('.assistant-list');list.scrollTop=220;
    shellFixture.input=document.querySelector('#pot-composer');shellFixture.search=document.querySelector('.assistant-search');shellFixture.listTop=list.scrollTop;
  });
  await settle();
  await page.evaluate(()=>{
    const history=document.querySelector('.history'),top=history.getBoundingClientRect().top;
    const node=[...history.querySelectorAll('[data-scroll-anchor]')].find(node=>node.getBoundingClientRect().bottom>top);
    shellFixture.anchor={key:node.dataset.scrollAnchor,offset:node.getBoundingClientRect().top-top};
  });
  const reads=await page.evaluate(()=>shellFixture.reads);
  await page.getByRole('button',{name:'Collapse assistant list',exact:true}).click();await settle();
  check(!await pane.isVisible(),'Collapse hides the assistant list');
  check(await page.getByRole('button',{name:'Expand assistant list',exact:true}).getAttribute('aria-expanded')==='false','Collapsed trigger exposes accessible state');
  check(await page.evaluate(()=>document.activeElement?.getAttribute('aria-label')==='Expand assistant list'),'Keyboard focus moves to the expand trigger');
  check(await page.evaluate(()=>{const h=document.querySelector('.history'),a=shellFixture.anchor,n=[...h.querySelectorAll('[data-scroll-anchor]')].find(n=>n.dataset.scrollAnchor===a.key);return n&&Math.abs(n.getBoundingClientRect().top-h.getBoundingClientRect().top-a.offset)<3;}),'Collapse retains the visible reading anchor');
  await page.getByRole('button',{name:'Expand assistant list',exact:true}).click();await settle();
  check(await input.inputValue()==='Keep this draft 中文','Fold and expand retain the draft');
  check(await page.evaluate(()=>shellFixture.input===document.querySelector('#pot-composer')&&shellFixture.search===document.querySelector('.assistant-search')&&shellFixture.search.value==='Assistant'&&document.querySelector('.assistant-list').scrollTop===shellFixture.listTop),'Composer, search control and directory scroll retain identity and state');
  check(await page.evaluate(()=>shellFixture.reads)===reads,'Folding causes no backend reads');
  await page.getByRole('button',{name:'Assistant information',exact:true}).click();
  check(await page.locator('.assistant-profile').isVisible(),'Avatar opens assistant information');
  await page.locator('.titlebar').click({position:{x:400,y:12}});
  check(!await page.locator('.assistant-profile').isVisible(),'Outside click dismisses assistant information');
  await page.getByRole('button',{name:'Assistant information',exact:true}).press('Enter');
  check(await page.locator('.assistant-profile').isVisible(),'Keyboard activation opens assistant information');
  await page.evaluate(()=>window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-locale',value:'zh-CN'}})));
  check(await page.locator('.assistant-profile').getAttribute('aria-label')==='助手信息'&&await page.locator('.assistant-profile-settings').innerText()==='助手设置','Open assistant menu updates its visible and accessible locale');
  await page.keyboard.press('Escape');
  check(!await page.locator('.assistant-profile').isVisible(),'Escape dismisses assistant information');
  await page.getByRole('button',{name:'助手信息',exact:true}).click();
  await page.locator('.assistant-profile-settings').click();
  await page.getByLabel('名称',{exact:true}).waitFor();
  check(await page.getByLabel('名称',{exact:true}).inputValue()==='Alpha','Avatar menu opens the selected assistant settings');
  await page.getByRole('button',{name:'返回',exact:true}).click();await input.waitFor({state:'visible'});
  check(await input.inputValue()==='Keep this draft 中文','Returning from settings retains the draft');
  await page.getByRole('button',{name:'收起助手列表',exact:true}).click();
  await page.getByRole('button',{name:'助手设置',exact:true}).click();await page.getByLabel('名称',{exact:true}).waitFor();
  await page.getByRole('button',{name:'返回',exact:true}).click();await input.waitFor({state:'visible'});
  check(!await pane.isVisible(),'Collapsed preference survives editor navigation');
  await page.setViewportSize({width:390,height:820});await settle();
  check(!await page.locator('.directory-toggle:visible').count(),'Wide fold controls stay out of phone navigation');
  await page.getByRole('button',{name:'返回助手列表',exact:true}).click();
  check(await pane.isVisible(),'Phone Back still opens the full assistant directory');
  await page.getByRole('searchbox',{name:'搜索助手'}).fill('');
  await page.locator('.assistant-row').first().click();await input.waitFor({state:'visible'});
  check(await input.inputValue()==='Keep this draft 中文','Phone navigation retains the selected conversation draft');
  await page.setViewportSize({width:1200,height:820});await settle();
  check(!await pane.isVisible(),'Returning to a wide window restores its collapsed state');
  await page.getByRole('button',{name:'展开助手列表',exact:true}).click();
  for(const width of [720,719,390]){
    await page.setViewportSize({width,height:820});await settle();
    check(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth&&document.querySelector('.history').scrollWidth<=document.querySelector('.history').clientWidth),'No horizontal overflow at '+width+'px');
  }
  await page.setViewportSize({width:1200,height:820});await settle();
  await page.evaluate(()=>document.documentElement.dataset.platform='desktop');
  check(await page.locator('.directory-heading > span').evaluate(node=>node.getBoundingClientRect().left)>=80,'Native directory heading avoids window controls');
  await page.getByRole('button',{name:'收起助手列表',exact:true}).click();
  check((await page.getByRole('button',{name:'展开助手列表',exact:true}).boundingBox()).x>=80,'Native collapsed header avoids window controls');
  await page.getByRole('button',{name:'展开助手列表',exact:true}).click();
  await page.evaluate(()=>{document.documentElement.dataset.platform='browser';window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key:'pot-ui-theme',value:'dark'}}));});
  check(await page.evaluate(()=>getComputedStyle(document.querySelector('.titlebar')).backgroundColor===getComputedStyle(document.querySelector('.pot-app')).backgroundColor),'Conversation header uses the dark conversation canvas');
  check(await page.evaluate(()=>shellFixture.sends)===0,'Layout and navigation never send a message');
  check(errors.length===0,'No browser exceptions');
  await page.evaluate(()=>shellFixture.dispose());
  return {checks,errors};
}
