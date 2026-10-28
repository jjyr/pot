import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {sourceServer,launchBrowser} from './browser-harness.mjs';

const baseline=process.argv.includes('--baseline'),output='output/pr-images/chat-delivery-layout';
const source=await sourceServer({root:process.env.POT_SOURCE_ROOT}),browser=await launchBrowser();
await mkdir(output,{recursive:true});
try{
 for(const width of [1280,390])for(const locale of ['en','zh-CN']){
  const context=await browser.newContext({viewport:{width,height:900},serviceWorkers:'block'}),page=await context.newPage(),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
   await page.goto(source.origin+'/scripts/tests/fixtures/work-records.html?locale='+locale+'&theme=light');
   await page.waitForFunction(()=>window.workRecordsFixture);
   if(width<720)await page.locator('.assistant-row').first().click();
   await page.evaluate(()=>workRecordsFixture.prepare());
   await page.waitForFunction(()=>document.querySelector('.history:not([data-preparation-surface]) .work-record-summary'));
   await page.waitForFunction(()=>{const history=document.querySelector('.history:not([data-preparation-surface])');return getComputedStyle(history).visibility==='visible'&&!history.hasAttribute('aria-busy');});
   await page.locator('.history:not([data-preparation-surface]) .work-record-summary').first().scrollIntoViewIfNeeded();
   const participants=await page.evaluate(()=>{const summary=document.querySelector('.history:not([data-preparation-surface]) .work-record-summary'),counts=summary.querySelector('.work-record-counts'),people=summary.querySelector('.work-record-participants');return {visible:[...people.children].filter(n=>n.classList.contains('work-participant')&&n.checkVisibility()).length,total:people.querySelectorAll('.work-participant').length,gap:people.getBoundingClientRect().left-counts.getBoundingClientRect().right,sameLine:Math.abs(people.getBoundingClientRect().top-counts.getBoundingClientRect().top)<10};});
   if(!baseline){assert.equal(participants.visible,participants.total,'Every communication counterpart is visible');if(participants.sameLine)assert(participants.gap<=20,'Counterparts follow communication count');}
   await page.screenshot({path:`${output}/work-${width}-${locale}-${baseline?'before':'after'}.png`});
   // An expired preparation deadline must never apply the document's minimum
   // placeholder height to a small decoded profile image on a later revision.
   const geometry=await page.evaluate(async()=>{
    const {avatar}=await import('/web/src/components/avatar.mjs');
    const {createPreparedRender,withPreparedRender,settlePreparedTree,createPreparationSurface,retirePreparedTree}=await import('/web/src/components/prepared-render.mjs');
    const {createMessageFile}=await import('/web/src/components/message-file.mjs');
    const png=document.querySelector('.avatar-image').src;
    const history=document.querySelector('.history:not([data-preparation-surface])'),host=document.createElement('div');history.replaceChildren(host);host.className='message-column';
    const surface=createPreparationSurface(history,host),member='late-avatar',deadlines=new Map([[JSON.stringify([member,'image',png]),performance.now()-1]]),context=createPreparedRender({member,deadlines});
    const row=document.createElement('article');row.className='message role-assistant';row.dataset.messageId=member;
    const speaker=document.createElement('div');speaker.className='message-speaker';speaker.append(avatar(25,png),'Pot');row.append(speaker);
    const file=withPreparedRender(context,member,()=>createMessageFile({href:'report.md',label:'Synthetic report',block:true},member,{messageFiles:async()=>[{state:'ready',href:'report.md',resource:{id:'synthetic',name:'report.md',entry:'report.md',mime_type:'text/markdown',size:3000,files:[{path:'report.md'}]}}],acquireResource:()=>({read:async()=>new Blob(['# Synthetic report\n\n'+('A paragraph for internal scrolling.\n\n'.repeat(80))]),release(){}}),downloadResource:async()=>{}}));
    const body=document.createElement('div');body.className='message-body';body.append(file);row.append(body);surface.host.append(row);
    const measurement=await settlePreparedTree(row,context).promise;host.append(row);await new Promise(requestAnimationFrame);
    const image=row.querySelector('.avatar-image'),resource=row.querySelector('pot-resource'),prose=row.querySelector('.resource-markdown');
    const outer=[file,resource].map(node=>({overflow:getComputedStyle(node).overflowY,client:node.clientHeight,scroll:node.scrollHeight}));
    prose.scrollTop=200;const scroll=prose.scrollTop,avatarHeight=image.getBoundingClientRect().height,liveHeight=row.getBoundingClientRect().height;
    window.layoutProbe={row,surface,retirePreparedTree};return {avatarHeight,liveHeight,preparedHeight:measurement.height,outer,scroll,notice:row.textContent.includes('Saved version from this reply')||row.textContent.includes('此回复保存的文件版本')};
   });
   await page.screenshot({path:`${output}/preview-${width}-${locale}-${baseline?'before':'after'}.png`});
   if(!baseline){assert.equal(geometry.avatarHeight,25,'Expired image deadline preserves avatar dimensions');assert(Math.abs(geometry.liveHeight-geometry.preparedHeight)<1,'Live height equals prepared height');assert(geometry.outer.every(node=>!['auto','scroll'].includes(node.overflow)),'Only preview content owns scrolling');assert(geometry.outer.every(node=>node.scroll<=node.client+2),'No clipped outer content');assert(!geometry.notice,'Saved-version notice removed');assert(geometry.scroll>0,'Preview still scrolls internally');}
   const inline=await page.evaluate(async locale=>{
    const {createPreparedRender,withPreparedRender,settlePreparedTree}=await import('/web/src/components/prepared-render.mjs');
    const {createMessageFile}=await import('/web/src/components/message-file.mjs');
    const paragraph=document.createElement('p');paragraph.style.cssText='font:16px/1.6 sans-serif';
    const before=document.createElement('span'),after=document.createElement('span');before.textContent=locale==='zh-CN'?'已整理好':'Ready: ';after.textContent='.';
    const context=createPreparedRender({member:'inline-file'});
    const file=withPreparedRender(context,'inline-file',()=>createMessageFile({href:'report.md',label:locale==='zh-CN'?'取证结论与剩余验收续接清单':'Evidence and remaining checks',block:false},'inline-file',{messageFiles:async()=>[{state:'ready',href:'report.md',resource:{id:'inline-report',name:'report.md'}}]}));
    paragraph.append(before,file,after);layoutProbe.row.querySelector('.message-body').prepend(paragraph);
    const measurement=await settlePreparedTree(paragraph,context).promise;
    const top=node=>{const range=document.createRange();range.selectNodeContents(node);return range.getBoundingClientRect().top;};
    return {before:top(before),link:top(file.link),after:top(after),preparedHeight:measurement.height,liveHeight:paragraph.getBoundingClientRect().height};
   },locale);
   if(!baseline){assert(Math.abs(inline.link-inline.before)<1,'Prepared file link aligns with preceding text baseline');assert(Math.abs(inline.link-inline.after)<1,'Prepared file link aligns with trailing punctuation');assert(Math.abs(inline.preparedHeight-inline.liveHeight)<1,'Inline link keeps its measured paragraph height');}
   assert.deepEqual(errors,[]);console.log(JSON.stringify({width,locale,baseline,participants,geometry,inline}));
  }finally{await context.close();}
 }
}finally{await browser.close();await source.close();}
