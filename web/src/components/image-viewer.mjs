import {translate} from '../locales/messages.mjs';

const node=(tag,cls='')=>{const element=document.createElement(tag);element.className=cls;return element;};

// The caller owns the authorized original and its lease for the whole dialog lifetime.
export function openImageViewer({image,label,trigger,download,onClose=()=>{}}) {
  const dialog=node('dialog','attachment-viewer image-viewer'),header=node('div','image-viewer-header'),title=node('strong','attachment-name'),toolbar=node('div','image-viewer-controls');
  const viewport=node('div','image-viewer-viewport'),plane=node('div','image-viewer-plane'),full=node('img'),zoom=node('output'),mode=node('select','control'),error=node('p','image-viewer-error');
  const t=key=>translate(document.documentElement.lang,`imageViewer.${key}`);
  const button=(cls,text,run)=>{const element=node('button',`${cls} control`);element.type='button';element.textContent=text;element.onclick=run;return element;};
  const close=button('file-image-close','×',()=>dispose()),out=button('image-viewer-zoom-out','−',()=>zoomBy(1/1.25)),inside=button('image-viewer-zoom-in','+',()=>zoomBy(1.25));
  const save=button('file-image-download','',async()=>{save.disabled=true;error.hidden=true;try{await download();}catch{if(!disposed){error.textContent=t('downloadFailed');error.hidden=false;}}finally{save.disabled=false;}});
  for(const value of ['width','window','original']){const option=node('option');option.value=value;mode.append(option);}
  const custom=node('option');custom.value='custom';custom.hidden=custom.disabled=true;mode.append(custom);
  title.id=`image-viewer-${crypto.randomUUID()}`;title.textContent=label;dialog.setAttribute('aria-labelledby',title.id);
  header.append(title,save,close);toolbar.append(mode,out,zoom,inside);dialog.append(header,toolbar,viewport,error);viewport.append(plane);plane.append(full);
  viewport.tabIndex=0;full.alt=label;full.draggable=false;full.src=image.currentSrc||image.src;error.setAttribute('role','alert');error.hidden=true;
  let scale=1,selection='width',disposed=false,drag=null;
  const width=image.naturalWidth,height=image.naturalHeight,minScale=.01,maxScale=Math.max(8,window.innerWidth/width);
  const oldOverflow=document.documentElement.style.overflow;
  function refresh(){
    close.setAttribute('aria-label',t('close'));save.textContent=t('download');out.setAttribute('aria-label',t('zoomOut'));inside.setAttribute('aria-label',t('zoomIn'));mode.setAttribute('aria-label',t('size'));zoom.setAttribute('aria-label',t('zoom'));
    for(const option of mode.options)option.textContent=t(option.value);
    viewport.setAttribute('aria-label',t('instructions'));viewport.title=t('instructions');
    if(!error.hidden)error.textContent=t('downloadFailed');
  }
  function layout(){
    plane.style.width=`${Math.max(viewport.clientWidth,width*scale)}px`;plane.style.height=`${Math.max(viewport.clientHeight,height*scale)}px`;
    full.style.width=`${width*scale}px`;full.style.height=`${height*scale}px`;
    full.style.left=`${Math.max(0,(viewport.clientWidth-width*scale)/2)}px`;
    zoom.value=new Intl.NumberFormat(document.documentElement.lang,{style:'percent',maximumFractionDigits:0}).format(scale);
    out.disabled=scale<=minScale;inside.disabled=scale>=maxScale;
  }
  function applyMode(reset=true){
    if(disposed)return;
    const left=viewport.scrollLeft/scale,top=viewport.scrollTop/scale;
    scale=selection==='original'?1:selection==='window'?Math.min(viewport.clientWidth/width,viewport.clientHeight/height):viewport.clientWidth/width;
    scale=Math.max(minScale,Math.min(maxScale,scale));layout();viewport.scrollTo(reset?0:left*scale,reset?0:top*scale);
  }
  // Preserve the image coordinate beneath the pointer (or viewport center).
  function zoomBy(factor,x=viewport.clientWidth/2,y=viewport.clientHeight/2){
    const next=Math.max(minScale,Math.min(maxScale,scale*factor));if(next===scale)return;
    const imageX=(viewport.scrollLeft+x-Math.max(0,(viewport.clientWidth-width*scale)/2))/scale,imageY=(viewport.scrollTop+y)/scale;
    scale=next;selection=mode.value='custom';layout();
    viewport.scrollLeft=imageX*scale+Math.max(0,(viewport.clientWidth-width*scale)/2)-x;viewport.scrollTop=imageY*scale-y;
  }
  mode.onchange=()=>{selection=mode.value;applyMode();};
  const events=new AbortController(),options={signal:events.signal};
  viewport.addEventListener('wheel',event=>{if(!event.ctrlKey)return;event.preventDefault();const rect=viewport.getBoundingClientRect();zoomBy(Math.exp(-event.deltaY*(event.deltaMode===1?.04:.002)),event.clientX-rect.left-viewport.clientLeft,event.clientY-rect.top-viewport.clientTop);},{...options,passive:false});
  viewport.addEventListener('pointerdown',event=>{
    // Touch keeps browser-native scrolling; mouse and pen drag the scroll surface.
    if(event.pointerType==='touch'||event.button!==0)return;
    event.preventDefault();viewport.focus({preventScroll:true});viewport.setPointerCapture(event.pointerId);
    drag={id:event.pointerId,x:event.clientX,y:event.clientY,left:viewport.scrollLeft,top:viewport.scrollTop};viewport.classList.add('is-dragging');
  },options);
  viewport.addEventListener('pointermove',event=>{if(drag?.id===event.pointerId){viewport.scrollLeft=drag.left+drag.x-event.clientX;viewport.scrollTop=drag.top+drag.y-event.clientY;}},options);
  const endDrag=()=>{const id=drag?.id;drag=null;if(id!==undefined&&viewport.hasPointerCapture(id))viewport.releasePointerCapture(id);viewport.classList.remove('is-dragging');};
  for(const type of ['pointerup','pointercancel','lostpointercapture'])viewport.addEventListener(type,endDrag,options);
  dialog.addEventListener('keydown',event=>{
    if(event.key==='Tab'){
      const stops=[...dialog.querySelectorAll('button:not(:disabled),select,[tabindex="0"]')],first=stops[0],last=stops.at(-1);
      if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus({preventScroll:true});}
      else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus({preventScroll:true});}
    }
    if(event.target===mode)return;
    if(['+','=','-'].includes(event.key)){event.preventDefault();zoomBy(event.key==='-'?1/1.25:1.25);}
  },options);
  dialog.addEventListener('cancel',event=>{event.preventDefault();dispose();},options);
  dialog.addEventListener('close',()=>dispose(),options);
  window.addEventListener('pot:locale',()=>{refresh();layout();},options);
  const resize=new ResizeObserver(()=>{if(selection==='custom')layout();else applyMode(false);});
  function dispose(){
    if(disposed)return;disposed=true;endDrag();events.abort();resize.disconnect();
    if(dialog.open)dialog.close();full.removeAttribute('src');dialog.remove();document.documentElement.style.overflow=oldOverflow;
    if(trigger?.isConnected)trigger.focus({preventScroll:true});onClose();
  }
  refresh();document.body.append(dialog);document.documentElement.style.overflow='hidden';dialog.showModal();applyMode();resize.observe(viewport);close.focus({preventScroll:true});
  return {dialog,close:dispose};
}
