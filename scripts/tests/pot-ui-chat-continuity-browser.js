// Ordinary production Chat with isolated IndexedDB and deterministic mixed heights.
// Real wheel input must preserve the same visible content through range changes.
async page=>{
  const origin=new URL(page.url()).origin,results=[],errors=[];
  const assert=(value,label)=>{if(!value)throw Error(label);};
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/chat-continuity-fixture',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><div id="app"></div></html>'}));
  for(const width of [1280,390,320]){
    await page.setViewportSize({width,height:800});
    await page.goto(origin+'/chat-continuity-fixture');
    await page.evaluate(async()=>{
      const {mountApp}=await import('/components/ui.js');
      const {createChatCache}=await import('/state/chat-cache.mjs');
      const {captureReadingPosition,readingAnchor}=await import('/components/reading-position.mjs');
      localStorage.setItem('pot-system-notifications-browser-v1','false');
      document.documentElement.dataset.platform='browser';
      const name='pot-continuity-'+crypto.randomUUID(),cache=createChatCache({name});
      let scope={space:'fixture',owner:'alpha'},receive,version=1,count=240,reads=0,imageReady;
      const imageURL=URL.createObjectURL(new Blob(['<svg xmlns="http://www.w3.org/2000/svg" width="600" height="420"><rect width="600" height="420" fill="#9ab9a9"/></svg>'],{type:'image/svg+xml'}));
      const image=new Promise(resolve=>{imageReady=()=>resolve(imageURL);});
      const message=index=>{
        const base={id:`m${index}`,sequence:String(index),version:1,status:'complete',text:'',role:index%3===1?'user':'assistant'};
        if(index%7===0)return {...base,role:'tool',tool:{id:`tool${index}`,turn_id:`turn${index}`,name:'inspect_records',args:{batch:index},phase:'completed',result:{content:[{type:'text',text:'Synthetic output line.\n'.repeat(350)}]}}};
        return {...base,text:`## Message ${index}\n\n`+'Readable synthetic evidence for this record. '.repeat(index%11===0?140:index%4+1)+(index%5===0?'\n\n```js\n'+Array.from({length:index%9+2},(_,i)=>`const record${i} = ${index};`).join('\n')+'\n```':'')};
      };
      const snapshot=()=>({kind:'reset',runtime_id:'fixture',boot_id:'boot',owner_id:JSON.stringify(['fixture',scope.owner]),conversation_id:scope.owner,cursor:String(version),replies:[],running:false,turn_id:null,messages:Array.from({length:80},(_,i)=>message(count-79+i)),history:{before:String(count-79),after:String(count),has_more:true,has_newer:false}});
      const client={chatCacheName:name,production:true,runtime:{runtime_id:'fixture',boot_id:'boot'},scope:()=>({...scope}),setScope:value=>scope={...scope,...value},changes:()=>()=>{},
        subscribe(callback,status){receive=callback;queueMicrotask(()=>{status('connected');callback(snapshot());});return()=>{};},
        async history(){reads++;return snapshot();},
        acquireFileImage:()=>({url:image,release(){}}),downloadFile:async()=>{},
        async request(path){const url=new URL(path,location.href);
          if(url.pathname==='/api/v1/spaces')return {active_space_id:'fixture',items:[{id:'fixture',name:'Fixture',kind:'local',control_id:'fixture',connected:true}]};
          if(url.pathname==='/api/v1/assistants')return {items:[{id:'alpha',name:'Scroll fixture'}]};
          return {items:[],pending:[],policy:{}};
        },
      };
      const dispose=mountApp(document.querySelector('#app'),client);
      const history=()=>document.querySelector('.history');
      function sample(){
        const h=history(),top=h.getBoundingClientRect().top,bottom=top+h.clientHeight;
        const mounted=[...h.querySelectorAll('article.message')];
        const visible=mounted.filter(row=>{const r=row.getBoundingClientRect();return r.height&&r.bottom>top&&r.top<bottom;}).map(row=>({id:row.dataset.messageId,sequence:row.dataset.sequence,top:row.getBoundingClientRect().top-top,bottom:row.getBoundingClientRect().bottom-top}));
        const indexes=[...h.querySelectorAll('[data-virtual-index]')].map(row=>Number(row.dataset.virtualIndex));
        return {top:h.scrollTop,height:h.scrollHeight,viewport:h.clientHeight,visible,anchor:readingAnchor(h),range:[Math.min(...indexes),Math.max(...indexes)],mounted:mounted.length};
      }
      const publish=messages=>{const base=String(version++);receive({...snapshot(),kind:'delta',base_cursor:base,messages});};
      window.continuity={sample,logs:[],get reads(){return reads;},
        async persisted(){return (await cache.read(JSON.stringify(['fixture','fixture','alpha']))).snapshot.messages.map(row=>row.sequence);},
        append(){count++;publish([message(count)]);},
        image(){const row=message(count);row.version=2;row.text+='\n\n![Synthetic image](pot-file://21cfe7c9-34cc-41a8-9156-39a8341ad3dc/landscape.png)';publish([row]);},imageReady,
        nested(){
          const parent={...message(++count),role:'tool',text:'',tool:{id:'nested-parent',turn_id:'nested-turn',name:'inspect_records',args:{},phase:'completed',result:{content:[{type:'text',text:'Parent result'}]}}};
          const children=Array.from({length:32},(_,i)=>({...message(++count),role:'tool',text:'',tool:{id:'nested-'+i,turn_id:'nested-turn',parent_id:'nested-parent',name:'inspect_records',args:{query:'Child '+i},phase:'completed',result:{content:[{type:'text',text:'Nested output.\n'.repeat(30+i)}]}}}));
          publish([parent,...children]);
        },
      };
      window.addEventListener('pagehide',()=>{dispose();cache.close();indexedDB.deleteDatabase(name);URL.revokeObjectURL(imageURL);},{once:true});
    });
    if(width<720)await page.getByRole('button',{name:'Scroll fixture',exact:true}).click();
    await page.locator('[data-message-id="m240"]').waitFor();
    await page.waitForTimeout(250);
    await page.mouse.move(width-35,400);
    let previous=await page.evaluate(()=>continuity.sample()),worst=0,maxMounted=0;
    const seen={up:new Set(),down:new Set()};
    for(let step=0;step<320;step++){
      const delta=step<160?-36:36,direction=delta<0?'up':'down';
      await page.mouse.wheel(0,delta);await page.waitForTimeout(55);
      const next=await page.evaluate(({step,delta})=>{const sample=continuity.sample();continuity.logs.push({step,delta,...sample});return sample;},{step,delta});
      const common=previous.visible.find(row=>next.visible.some(item=>item.id===row.id));
      assert(common,`${width}px ${direction} step ${step} skipped the entire visible interval`);
      const movement=next.visible.find(row=>row.id===common.id).top-common.top;
      const expected=delta>0?Math.min(delta,previous.height-previous.viewport-previous.top):Math.max(delta,-previous.top);
      const drift=Math.abs(movement+expected);worst=Math.max(worst,drift);maxMounted=Math.max(maxMounted,next.mounted);
      assert(drift<=3,`${width}px ${direction} step ${step}: visible content drifted ${drift.toFixed(1)}px for ${delta}px wheel input`);
      next.visible.forEach(row=>seen[direction].add(Number(row.sequence)));previous=next;
    }
    for(const direction of ['up','down'])for(let sequence=221;sequence<=239;sequence++)assert(seen[direction].has(sequence),`${width}px ${direction}: message ${sequence} was never readable`);
    assert(maxMounted<80,`${width}px retains virtualization`);
    assert(await page.evaluate(()=>continuity.reads)===0,`${width}px traversal does not fetch or reorder the stored page`);
    const persisted=await page.evaluate(()=>continuity.persisted());
    assert(persisted.length===80&&persisted.every((value,index)=>Number(value)===161+index),`${width}px stored messages stay complete and ordered`);
    // Output and page remounts preserve the paused reader, while Latest is explicit.
    for(let i=0;i<12;i++){await page.mouse.wheel(0,-36);await page.waitForTimeout(35);}
    const saved=await page.evaluate(()=>continuity.sample().anchor);
    await page.evaluate(()=>continuity.append());await page.waitForTimeout(180);
    const anchorOffset=()=>page.evaluate(id=>{const h=document.querySelector('.history'),row=h.querySelector(`[data-message-id="${id}"]`);return row?.getBoundingClientRect().top-h.getBoundingClientRect().top;},saved.id);
    assert(Math.abs(await anchorOffset()-saved.offset)<=3,`${width}px new output preserves reading`);
    if(width<720)await page.getByRole('button',{name:'Conversation actions',exact:true}).click();
    await page.getByRole('button',{name:'Settings',exact:true}).first().click();
    if(width<720)await page.locator('.titlebar .narrow-navigation').click();
    await page.getByRole('button',{name:'Chat',exact:true}).first().click();
    if(width<720)await page.getByRole('button',{name:'Scroll fixture',exact:true}).click();
    await page.waitForTimeout(180);
    assert(Math.abs(await anchorOffset()-saved.offset)<=3,`${width}px page switch preserves reading`);
    await page.locator('.latest-button').click();await page.waitForTimeout(180);
    let current=await page.evaluate(()=>continuity.sample());assert(current.height-current.viewport-current.top<=2,`${width}px Latest follows the end`);
    await page.evaluate(()=>continuity.image());await page.locator('pot-file-image[data-state="loading"]').waitFor();
    await page.mouse.move(width-35,400);
    for(let i=0;i<6;i++){await page.mouse.wheel(0,-36);await page.waitForTimeout(35);}
    const imageAnchor=await page.evaluate(()=>continuity.sample().anchor);
    await page.evaluate(()=>continuity.imageReady());await page.locator('pot-file-image[data-state="ready"]').waitFor();await page.waitForTimeout(120);
    const imageOffset=await page.evaluate(id=>{const h=document.querySelector('.history');return h.querySelector(`[data-message-id="${id}"]`).getBoundingClientRect().top-h.getBoundingClientRect().top;},imageAnchor.id);
    assert(Math.abs(imageOffset-imageAnchor.offset)<=3,`${width}px asynchronous image growth preserves reading`);
    await page.locator('.latest-button').click();await page.waitForTimeout(120);
    current=await page.evaluate(()=>continuity.sample());assert(current.height-current.viewport-current.top<=2,`${width}px Latest reveals the loaded image`);
    await page.evaluate(()=>continuity.nested());await page.waitForTimeout(180);
    const parent=page.locator('[data-message-id="m242"] > .message-body > details.tool-call > summary');
    await parent.click();await page.waitForTimeout(150);
    assert(await page.locator('.tool-children article.message').count()>0,`${width}px nested tools render their child window`);
    await page.mouse.move(width-35,400);
    let nestedPrevious=await page.evaluate(()=>continuity.sample()),nestedWorst=0;
    for(let step=0;step<40;step++){
      const delta=step<20?36:-36;await page.mouse.wheel(0,delta);await page.waitForTimeout(55);
      const next=await page.evaluate(()=>continuity.sample());
      const common=nestedPrevious.visible.find(row=>row.id!=='m242'&&next.visible.some(item=>item.id===row.id));
      if(nestedPrevious.visible.some(row=>row.id!=='m242')&&next.visible.some(row=>row.id!=='m242'))assert(common,`${width}px nested child window skipped its visible interval at step ${step}`);
      if(common){const actual=next.visible.find(row=>row.id===common.id).top-common.top;const expected=delta>0?Math.min(delta,nestedPrevious.height-nestedPrevious.viewport-nestedPrevious.top):Math.max(delta,-nestedPrevious.top);nestedWorst=Math.max(nestedWorst,Math.abs(actual+expected));assert(Math.abs(actual+expected)<=3,`${width}px nested child window jumped at step ${step}`);}
      nestedPrevious=next;
    }
    const childID=await page.locator('.tool-children article.message').last().getAttribute('data-message-id');
    const child=page.locator(`[data-message-id="${childID}"] > .message-body > details.tool-call`);
    await child.locator(':scope > summary').click();await page.waitForTimeout(100);
    assert((await child.locator('.tool-body').textContent()).includes('Nested output.'),`${width}px expanding a child reveals its long output`);
    await child.locator(':scope > summary').click();
    await parent.click();await page.waitForTimeout(100);
    results.push({width,worstDrift:worst,nestedWorst,maxMounted,readable:[221,239],steps:320});
  }
  assert(errors.length===0,errors.join('; '));
  return {passed:true,results,errors};
}
