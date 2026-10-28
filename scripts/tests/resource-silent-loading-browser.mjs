import assert from 'node:assert/strict';
import {sourceServer,launchBrowser} from './browser-harness.mjs';

const source=await sourceServer(),browser=await launchBrowser();
try{
 for(const width of [1280,390])for(const locale of ['en','zh-CN']){
  const page=await browser.newPage({viewport:{width,height:852}}),errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try{
   await page.goto(source.origin+'/empty-silent-loading-fixture');
   const result=await page.evaluate(async locale=>{
    document.documentElement.lang=locale;document.body.replaceChildren();
    const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/styles.css';document.head.append(sheet);await new Promise(resolve=>sheet.onload=resolve);
    const {createMessageFile}=await import('/components/message-file.mjs');
    const {createResourceView}=await import('/components/resource-view.mjs');
    const {createFileImage}=await import('/components/file-image.mjs');
    const {createMermaidBlock}=await import('/components/mermaid.mjs');
    const {createPreparedRender,createPreparationBudget,withPreparedRender,retirePreparedTree}=await import('/components/prepared-render.mjs');
    const {translate}=await import('/locales/messages.mjs');
    const check=(value,message)=>{if(!value)throw Error(message);};
    const frames=async()=>{for(let i=0;i<3;i++)await new Promise(requestAnimationFrame);};
    const resource={id:'silent',name:'report.txt',entry:'report.txt',mime_type:'text/plain',size:10,files:[{path:'report.txt'}]};
    let bound=false,contentReady,imageFail;
    const content=new Promise(resolve=>contentReady=resolve),image=new Promise((_,reject)=>imageFail=reject);
    const options={messageFiles:async()=>[{href:'report.txt',state:bound?'ready':'resolving',...(bound?{resource}:{})}],acquireResource:()=>({read:()=>content,release(){}}),downloadResource:async()=>{}};
    const paragraph=document.createElement('p');paragraph.style.cssText='font:16px/1.6 sans-serif';
    const link=createMessageFile({href:'report.txt',label:'Report',block:false},'silent',options);paragraph.append('See ',link,'.');document.body.append(paragraph);await frames();
    const pendingHeight=paragraph.getBoundingClientRect().height;
    check(!link.status&&link.textContent==='Report','Pending binding keeps only the original link label');
    const preview=createResourceView({resource,mode:'preview'},options),picture=createFileImage({path:'image.png'},'Image',{acquireFileImage:()=>({url:image,release(){}}),downloadFile:async()=>{}}),diagram=createMermaidBlock('graph TD\n A',document.createElement('pre'),{closed:false});
    document.body.append(preview,picture,diagram);
    const queued=document.createElement('div'),context=createPreparedRender({budget:createPreparationBudget(0)});queued.preparedContext=context.connectionContext;
    withPreparedRender(context,'queued',()=>queued.append(createFileImage({path:'queued.png'},'Queued image',{acquireFileImage(){throw Error('Queued image should not start');}})));document.body.append(queued);await frames();
    check(queued.querySelector('.resource-preparation-placeholder').getBoundingClientRect().height>0,'Silent queued placeholder remains measurable');
    for(const key of ['resource.loading','fileReference.pending','fileReference.resolving','markdown.imageLoading','markdown.diagramLoading'])check(!document.body.textContent.includes(translate(locale,key)),'No transient text: '+key);
    check(preview.querySelector(':scope > .resource-status').hidden&&picture.querySelector('.file-image-status').hidden,'Loading statuses do not occupy a line');
    bound=true;await link.load();check(paragraph.getBoundingClientRect().height===pendingHeight,'Binding completion does not change inline paragraph height');
    link.open();const dialog=link.dialog;await Promise.all(dialog.getAnimations().map(animation=>animation.finished));await frames();const box=dialog.getBoundingClientRect();
    contentReady(new Blob(['A ready preview.\n'.repeat(100)]));
    const deadline=performance.now()+5000;while(dialog.querySelector('pot-resource').dataset.state!=='ready'){check(performance.now()<deadline,'Preview timed out');await frames();}
    await frames();const readyBox=dialog.getBoundingClientRect();check(box.top===readyBox.top&&box.height===readyBox.height,'Preview dialog does not resize or recenter after loading '+JSON.stringify({before:box.toJSON(),after:readyBox.toJSON(),style:getComputedStyle(dialog).cssText,height:getComputedStyle(dialog).height}));
    dialog.close();await frames();
    imageFail(Error('Synthetic offline'));await frames();check(picture.dataset.state==='error'&&!picture.querySelector('.file-image-status').hidden&&picture.textContent.includes(translate(locale,'markdown.imageUnavailable')),'Real failures remain visible');
    for(const element of [paragraph,preview,picture,diagram,queued])retirePreparedTree(element);
    return {pendingHeight,dialogHeight:readyBox.height,dialogTop:readyBox.top};
   },locale);
   assert.deepEqual(errors,[]);console.log(JSON.stringify({width,locale,passed:true,...result}));
  }finally{await page.close();}
 }
}finally{await browser.close();await source.close();}
