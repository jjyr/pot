// Real window commits with synthetic resource leases. The HTTP runner separately
// covers transport/IDB; this matrix checks renderer readiness and node ownership.
export async function preparedResourceMatrix(page){
 return page.evaluate(async()=>{
  const {createMessageWindow}=await import('/components/message-window.mjs');
  const {createResourceView}=await import('/components/resource-view.mjs');
  const {createMessageFile}=await import('/components/message-file.mjs');
  const {retirePreparedTree}=await import('/components/prepared-render.mjs');
  const f=preparedFixture,results=[],frames=async(count=3)=>{for(let i=0;i<count;i++)await new Promise(requestAnimationFrame);};
  const wait=async(test)=>{const deadline=performance.now()+12000;while(!test()){if(performance.now()>deadline)throw Error('Prepared resource matrix did not settle '+JSON.stringify({top:f.scroller.scrollTop,extent:f.scroller.scrollHeight,active:document.activeElement?.outerHTML.slice(0,200),rows:[...f.host.querySelectorAll('article')].map(row=>row.dataset.messageId),surfaces:document.querySelectorAll('[data-preparation-surface]').length}));await frames(1);}};
  for(const width of [700,340]){
   f.scroller.style.width=width+'px';f.host.replaceChildren();
   let acquired=0,released=0,decodeRelease,bindingRelease;
   const decode=HTMLImageElement.prototype.decode,decodeGate=new Promise(resolve=>decodeRelease=resolve),bindingGate=new Promise(resolve=>bindingRelease=resolve),urls=[];
   HTMLImageElement.prototype.decode=function(){const ready=decode.call(this);return this.alt==='Matrix image'?ready.then(()=>decodeGate):ready;};
   const png='data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==';
   const resource=(kind)=>{const mime={image:'image/png',pdf:'application/pdf',html:'text/html',markdown:'text/markdown',binding:'text/plain'}[kind],path=kind+'.'+({image:'png',pdf:'pdf',html:'html',markdown:'md',binding:'txt'}[kind]);return {id:'matrix-'+kind,name:path,entry:path,mime_type:mime,size:1,files:[{path,mime_type:mime,size:1,digest:'matrix'}]};};
   const options={downloadResource:async()=>{},messageFiles:()=>bindingGate,acquireResource:value=>{
    acquired++;let done=false;return {release(){if(!done){done=true;released++;}},read:async()=>new Blob([value.mime_type==='text/markdown'?'# Nested ready\n\n```mermaid\ngraph TD\n A --> B\n```':'Bound resource'],{type:value.mime_type}),url:()=>{
     if(value.mime_type==='image/png')return png;
     const url=URL.createObjectURL(new Blob(['%PDF-1.4\n1 0 obj <</Type /Catalog /Pages 2 0 R>> endobj\n2 0 obj <</Type /Pages /Kids [] /Count 0>> endobj\ntrailer <</Root 1 0 R>>\n%%EOF'],{type:'application/pdf'}));urls.push(url);return url;
    },renderDocument:async token=>{
     const script=`const send=(action,value)=>parent.postMessage({type:'pot-resource',token:${JSON.stringify(token)},action,value},'*');send('resize',180);send('ready');addEventListener('message',event=>{if(event.data==='grow')send('resize',900)});`;
     return '<!doctype html><p>Isolated synthetic HTML</p><script>'+script+'</script>';
    },trackDocumentURL:url=>urls.push(url)};
   }};
   const rows=Array.from({length:100},(_,index)=>({id:'matrix-'+index,viewKey:'matrix-'+index,role:'assistant',text:'Synthetic row '+index,...(index===99?{resources:true}:{})}));
   let window;const render=(message,old)=>{
    if(old)return old;
    const row=document.createElement('article');row.className='message role-assistant';const body=document.createElement('div');body.className='message-body';row.append(body);const paragraph=document.createElement('p');paragraph.textContent=message.text;body.append(paragraph);
    if(message.resources){for(const kind of ['image','pdf','html','markdown'])body.append(createResourceView({resource:resource(kind),mode:'preview',caption:kind==='image'?'Matrix image':'Matrix '+kind},{...options,renderSlot:kind}));body.append(createMessageFile({href:'binding.txt',label:'Delayed binding',block:true},message.id,options));}
    return {row};
   };
   try{
    window=createMessageWindow({scroller:f.scroller,host:f.host,render,dispose:record=>retirePreparedTree(record.row),requestRender:()=>window.setMessages(rows)});
    const start=performance.now();window.setMessages(rows);await frames(6);
    if(window.prepared||f.host.querySelector('pot-resource'))throw Error('Image decoding and delayed binding must gate the initial actual window');
    const held={elapsed:performance.now()-start,acquired,liveResources:f.host.querySelectorAll('pot-resource').length};
    decodeRelease();bindingRelease([{id:'matrix-binding',href:'binding.txt',state:'ready',resource:resource('binding')}]);await wait(()=>window.prepared);await frames();
    const live=f.host.querySelector('[data-message-id="matrix-99"]'),nodes=[...live.querySelectorAll('pot-resource')],diagram=live.querySelector('pot-mermaid'),height=live.getBoundingClientRect().height;
    if(nodes.length!==5||nodes.some(node=>node.dataset.state!=='ready')||diagram?.dataset.state!=='ready')throw Error('Recursive ready includes image decode, PDF frame, isolated HTML and nested Markdown diagram');
    if(acquired!==5||released!==0)throw Error('Initial candidate transfer restarted a lease');
    live.querySelector('iframe.resource-interactive').contentWindow.postMessage('grow','*');await wait(()=>live.querySelector('iframe.resource-interactive').style.getPropertyValue('--resource-height')==='900px'&&window.prepared);await frames();
    if(live.getBoundingClientRect().height!==height)throw Error('Late isolated HTML resize changed the committed row frame');
    const ready={elapsed:performance.now()-start,height,acquired,released,kinds:nodes.map(node=>node.dataset.kind),sameNodes:nodes.every(node=>node.isConnected)};
    document.body.tabIndex=-1;document.body.focus({preventScroll:true});window.locate({id:'matrix-0'});await wait(()=>window.prepared&&!live.isConnected);
    if(released!==5)throw Error('Virtual removal did not release each resource exactly once');
    window.scrollToLatest();await wait(()=>window.prepared&&!!f.host.querySelector('[data-message-id="matrix-99"] pot-mermaid[data-state="ready"]'));await frames();
    const remounted=f.host.querySelector('[data-message-id="matrix-99"]');if(remounted===live||acquired!==10||released!==5)throw Error('Actual remount did not prepare new instances with balanced ownership');
    results.push({width,held,ready,remount:{height:remounted.getBoundingClientRect().height,acquired,released}});
   }finally{window?.close();HTMLImageElement.prototype.decode=decode;for(const url of urls)URL.revokeObjectURL(url);}
   if(released!==acquired)throw Error('Closing the resource window leaked leases');
  }
  return results;
 });
}

export async function preparedLateResources(page){
 let releaseHTML;const htmlGate=new Promise(resolve=>releaseHTML=resolve);
 const route=async request=>{await htmlGate;const token=new URL(request.request().url()).searchParams.get('token');await request.fulfill({contentType:'text/html',body:'<!doctype html><p>Late isolated document</p><script>parent.postMessage({type:"pot-resource",token:'+JSON.stringify(token)+',action:"resize",value:900},"*");parent.postMessage({type:"pot-resource",token:'+JSON.stringify(token)+',action:"ready"},"*")</script>'});};
 await page.route('**/prepared-slow-html?*',route);
 try{
  const initial=await page.evaluate(async()=>{
   const f=preparedFixture,{createResourceView}=await import('/components/resource-view.mjs'),context=f.createPreparedRender(),row=document.createElement('article'),releases=[];
   row.className='message';row.dataset.messageId='late-types';row.preparedContext=context.connectionContext;
   const state=globalThis.lateResourceMatrix={row,context,acquired:0,released:0,releases};
   f.withPreparedRender(context,'late-types',()=>{for(const kind of ['image','pdf','html']){
    const mime={image:'image/png',pdf:'application/pdf',html:'text/html'}[kind],resource={id:'late-'+kind,name:kind,entry:kind,mime_type:mime,size:1,files:[{path:kind,mime_type:mime,size:1,digest:'late'}]};
    row.append(createResourceView({resource,mode:'preview',caption:'Late '+kind},{renderSlot:kind,downloadResource:async()=>{},acquireResource:()=>{
     state.acquired++;return {release:()=>state.released++,url:()=>new Promise(resolve=>releases.push(()=>resolve(kind==='image'?'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==':'data:application/pdf;base64,'+btoa('%PDF-1.4\n1 0 obj <</Type /Catalog /Pages 2 0 R>> endobj\n2 0 obj <</Type /Pages /Kids [] /Count 0>> endobj\ntrailer <</Root 1 0 R>>\n%%EOF')))),renderURL:token=>location.origin+'/prepared-slow-html?token='+encodeURIComponent(token)};
    }}));
   }});
   f.surface.host.append(row);const start=performance.now(),measurement=await f.settlePreparedTree(row,context).promise;
   context.phase='live';f.moveConversationNode(f.host,row);await new Promise(requestAnimationFrame);
   const result={elapsed:performance.now()-start,measurement:measurement.height,height:row.getBoundingClientRect().height,frames:[...row.querySelectorAll('pot-resource')].map(node=>({kind:node.dataset.kind,height:node.getBoundingClientRect().height,fallback:node.dataset.preparedFallback,state:node.dataset.state})),acquired:state.acquired};
   state.initial=result;return result;
  });
  releaseHTML();
  const late=await page.evaluate(async()=>{
   const f=preparedFixture,state=lateResourceMatrix;for(const release of state.releases)release();
   const deadline=performance.now()+12000;while([...state.row.querySelectorAll('pot-resource')].some(node=>node.dataset.state!=='ready')){if(performance.now()>deadline)throw Error('Late preview types did not become ready');await new Promise(requestAnimationFrame);}
   for(let i=0;i<3;i++)await new Promise(requestAnimationFrame);
   const result={height:state.row.getBoundingClientRect().height,frames:[...state.row.querySelectorAll('pot-resource')].map(node=>({kind:node.dataset.kind,height:node.getBoundingClientRect().height,state:node.dataset.state})),htmlHeight:state.row.querySelector('.resource-interactive').style.getPropertyValue('--resource-height')};
   f.retirePreparedTree(state.row);await Promise.resolve();return {...result,acquired:state.acquired,released:state.released};
  });
  if(initial.elapsed<4900||initial.elapsed>6500||initial.frames.some(frame=>frame.height<240||frame.fallback!=='true'||frame.state!=='loading'))throw Error('Every unresolved preview type must use its original five-second fixed frame: '+JSON.stringify(initial));
  if(late.height!==initial.height||late.frames.some(frame=>frame.height!==initial.frames.find(before=>before.kind===frame.kind).height||frame.state!=='ready')||late.acquired!==3||late.released!==3||late.htmlHeight!=='900px')throw Error('Late image/PDF/HTML completion changed the published frame or leaked a lease: '+JSON.stringify({initial,late}));
  return {initial,late};
 }finally{releaseHTML();await page.unroute('**/prepared-slow-html?*',route);}
}
