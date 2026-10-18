import {icon} from './icons.mjs';

export const attachmentLimit = 8 * 1024 * 1024;
export const attachmentTypes = {image:['image/png','image/jpeg','image/webp'],pdf:['application/pdf']};
export function attachmentKind(mime) {return attachmentTypes.image.includes(mime)?'image':mime==='application/pdf'?'pdf':null;}
export function attachmentAllowed(caps,mime) {return !!caps?.[attachmentKind(mime)];}
export function attachmentProblem(items,caps,zh=false) {
  for(const item of items) {
    if(!attachmentAllowed(caps,item.attachment.mime_type))return zh?(attachmentKind(item.attachment.mime_type)==='image'?'当前模型不支持图片':'当前模型不支持该类型'):(attachmentKind(item.attachment.mime_type)==='image'?'Current model does not support images':'Current model does not support this file type');
    if(item.phase!=='ready')return item.phase==='failed'?(zh?'附件上传失败，请重试或移除':'Attachment upload failed; retry or remove'):(zh?'等待附件上传完成':'Waiting for attachments to finish uploading');
  }
  return '';
}
export function failedAttachmentInput(snapshot) {
  if(!snapshot||snapshot.running)return null;
  const messages=snapshot.messages||[],anchor=messages.findLast(m=>m.role==='user'&&!m.steering);
  if(anchor?.status!=='failed')return null;
  const input=messages.slice(messages.indexOf(anchor)).findLast(m=>m.role==='user'&&m.attachments?.length);
  return input?{input,target:anchor.id}:null;
}
export function formatAttachmentSize(size) {return size<1024?`${size} B`:size<1024*1024?`${(size/1024).toFixed(1)} KB`:`${(size/1024/1024).toFixed(1)} MB`;}
const node=(tag,cls='',text)=>{const e=document.createElement(tag);e.className=cls;if(text!==undefined)e.textContent=text;return e;};
const action=(label,run)=>{const b=node('button','control',label);b.type='button';b.onclick=run;return b;};
const mimeOf=file=>file.type||({'png':'image/png','jpg':'image/jpeg','jpeg':'image/jpeg','webp':'image/webp','pdf':'application/pdf'}[file.name.split('.').at(-1).toLowerCase()]||'');

// One shared byte-upload UI for DOM File objects in browsers and Wails WebViews.
// Every asynchronous operation captures its owner/Space before starting.
export function createAttachmentComposer({client,form,input,getKey,getCaps,canAdd,isSending=()=>false,changed,locale}) {
  const drafts=new Map(),urls=new Set();let disposed=false,signature='';
  const dropTarget=form.closest('.workspace')||form;
  const strip=node('div','composer-attachments'),hint=node('div','attachment-hint'),toolbar=node('div','composer-toolbar'),menu=node('div','attachment-menu');
  hint.setAttribute('role','status');menu.hidden=true;
  const plus=action('',()=>{menu.hidden=!menu.hidden;});plus.className='attachment-plus control';plus.append(icon('plus'));plus.setAttribute('aria-haspopup','menu');
  const picker=node('input');picker.type='file';picker.multiple=true;picker.hidden=true;
  const pickImage=action('',()=>pick('image')),pickPDF=action('',()=>pick('pdf'));menu.append(pickImage,pickPDF);menu.setAttribute('role','menu');
  for(const b of [pickImage,pickPDF])b.setAttribute('role','menuitem');
  toolbar.append(plus,menu,picker);form.prepend(strip);form.append(hint,toolbar);
  const list=(key=getKey())=>drafts.get(key)||[];
  const set=(key,items)=>{drafts.set(key,items);signature='';if(!disposed)changed();};
  const zh=()=>locale()==='zh-CN';
  function message(text){hint.textContent=text;hint.hidden=false;}
  function pick(kind){picker.accept=attachmentTypes[kind].join(',');picker.value='';menu.hidden=true;picker.click();}
  function remove(item,key){
    if(isSending())return;
    item.removed=true;item.abort?.abort();set(key,list(key).filter(a=>a!==item));
    if(item.url){URL.revokeObjectURL(item.url);urls.delete(item.url);}
    // A chunk may already have completed after cancellation. Remove only after
    // the original operation has settled; never race it with a new upload.
    Promise.resolve(item.pending).finally(()=>client.request('/api/v1/attachments',{...item.scope,method:'POST',body:{action:'remove',id:item.attachment.id}})).catch(()=>{});
  }
  async function upload(item,key){
    if(item.pending&&item.phase==='uploading')return;
    item.phase='uploading';item.error='';item.abort=new AbortController();signature='';changed();
    item.pending=(async()=>{
      const send=body=>client.request('/api/v1/attachments',{...item.scope,method:'POST',body,signal:item.abort.signal});
      let status=await send({action:'begin',id:item.attachment.id,name:item.file.name,mime_type:item.attachment.mime_type,size:item.file.size});
      if(!status.ready){
        for(let offset=status.offset;offset<item.file.size;){
          const bytes=new Uint8Array(await item.file.slice(offset,offset+192*1024).arrayBuffer());
          let binary='';for(let i=0;i<bytes.length;i+=8192)binary+=String.fromCharCode(...bytes.subarray(i,i+8192));
          status=await send({action:'chunk',id:item.attachment.id,offset,data:btoa(binary)});
          if(status.offset<=offset||status.offset>item.file.size)throw Error('Invalid attachment upload offset');
          offset=status.offset;item.progress=offset/item.file.size;signature='';if(!disposed)changed();
        }
        status=await send({action:'finish',id:item.attachment.id});
      }
      if(!status.ready)throw Error('Attachment upload did not finish');
      item.attachment=status.attachment;item.phase='ready';item.progress=1;
    })().catch(reason=>{if(!item.removed){item.phase='failed';item.error=reason.message||String(reason);}}).finally(()=>{signature='';if(!disposed)changed();});
    await item.pending;
  }
  function add(files){
    const key=getKey(),caps=getCaps();if(!key||!canAdd())return;
    const items=[...list(key)],scope={...client.scope()};let rejection='';
    for(const file of files){
      const mime=mimeOf(file);
      if(!attachmentAllowed(caps,mime)){rejection=attachmentKind(mime)==='image'?(zh()?'当前模型不支持图片':'Current model does not support images'):(zh()?'当前模型不支持该类型':'Current model does not support this file type');continue;}
      if(!file.size||items.length>=8||items.reduce((n,a)=>n+a.attachment.size,0)+file.size>attachmentLimit){rejection=zh()?'最多 8 个附件，总大小不超过 8 MiB':'Up to 8 attachments, totaling at most 8 MiB';continue;}
      const item={attachment:{id:crypto.randomUUID(),name:file.name,mime_type:mime,size:file.size},file,scope,phase:'uploading',progress:0};
      if(attachmentKind(mime)==='image'){item.url=URL.createObjectURL(file);urls.add(item.url);}
      items.push(item);upload(item,key);
    }
    set(key,items);if(rejection)message(rejection);
  }
  const select=()=>add([...picker.files]);picker.addEventListener('change',select);
  const drag=event=>{if([...event.dataTransfer?.types||[]].includes('Files')){event.preventDefault();form.classList.add('attachment-drag');}};
  const leave=event=>{if(!dropTarget.contains(event.relatedTarget))form.classList.remove('attachment-drag');};
  const drop=event=>{if(event.dataTransfer?.files?.length){event.preventDefault();form.classList.remove('attachment-drag');add([...event.dataTransfer.files]);}};
  const paste=event=>{const files=[...event.clipboardData?.files||[]];if(files.length){event.preventDefault();add(files);}};
  dropTarget.addEventListener('dragover',drag);dropTarget.addEventListener('dragleave',leave);dropTarget.addEventListener('drop',drop);input.addEventListener('paste',paste);
  // Prevent a dropped file outside the composer from navigating the WebView.
  const preventNavigation=event=>{if([...event.dataTransfer?.types||[]].includes('Files'))event.preventDefault();};
  document.addEventListener('dragover',preventNavigation);document.addEventListener('drop',preventNavigation);
  return {
    toolbar,
    items:list,
    refs:()=>list().map(item=>item.attachment),
    problem:()=>attachmentProblem(list(),getCaps(),zh()),
    clearSubmitted(key,refs){const ids=new Set(refs.map(a=>a.id));for(const item of list(key))if(ids.has(item.attachment.id)&&item.url){URL.revokeObjectURL(item.url);urls.delete(item.url);}set(key,list(key).filter(item=>!ids.has(item.attachment.id)));},
    restore(key,refs,scope){if(list(key).length)return;set(key,refs.map(attachment=>({attachment,scope,phase:'ready',progress:1})));},
    matches(refs){return JSON.stringify(this.refs())===JSON.stringify(refs||[]);},
    update(){
      const caps=getCaps(),items=list(),key=getKey();plus.title=zh()?'添加附件':'Add attachment';plus.setAttribute('aria-label',plus.title);
      plus.disabled=!canAdd()||!(caps?.image||caps?.pdf);if(plus.disabled)menu.hidden=true;
      pickImage.textContent=zh()?'图片':'Images';pickPDF.textContent='PDF';pickImage.disabled=!caps?.image;pickPDF.disabled=!caps?.pdf;
      pickImage.title=caps?.image?'':(zh()?'当前模型不支持图片':'Current model does not support images');pickPDF.title=caps?.pdf?'':(zh()?'当前模型不支持该类型':'Current model does not support this file type');
      const next=JSON.stringify([key,items.map(i=>[i.attachment.id,i.phase,i.progress,i.error]),caps,zh(),isSending()]);
      if(next===signature)return;signature=next;strip.replaceChildren();strip.hidden=!items.length;
      hint.textContent=attachmentProblem(items,caps,zh());hint.hidden=!hint.textContent;
      for(const item of items){
        const card=node('div',`attachment-card ${attachmentKind(item.attachment.mime_type)==='image'?'is-image':''}`);
        if(attachmentKind(item.attachment.mime_type)==='image'){
          const image=node('img');image.alt=item.attachment.name;card.append(image);
          if(item.url)image.src=item.url;
          else client.attachmentURL(item.attachment,item.scope).then(url=>{if(image.isConnected)image.src=url;}).catch(()=>{});
        }
        const info=node('div','attachment-info');info.append(node('span','attachment-name',item.attachment.name),node('span','attachment-size',formatAttachmentSize(item.attachment.size)));card.append(info);
        const close=action('×',()=>remove(item,key));close.className='attachment-remove control';close.setAttribute('aria-label',zh()?'移除附件':'Remove attachment');close.disabled=isSending();card.append(close);
        if(item.phase==='uploading'){const progress=node('progress');progress.max=1;progress.value=item.progress;progress.setAttribute('aria-label',zh()?'上传进度':'Upload progress');card.append(progress);}
        if(item.phase==='failed'){card.classList.add('has-error');card.append(node('span','attachment-error',item.error),action(zh()?'重试':'Retry',()=>upload(item,key)));}
        if(!attachmentAllowed(caps,item.attachment.mime_type))card.classList.add('is-incompatible');
        strip.append(card);
      }
    },
    destroy(){disposed=true;for(const items of drafts.values())for(const item of items)item.abort?.abort();for(const url of urls)URL.revokeObjectURL(url);dropTarget.removeEventListener('dragover',drag);dropTarget.removeEventListener('dragleave',leave);dropTarget.removeEventListener('drop',drop);input.removeEventListener('paste',paste);document.removeEventListener('dragover',preventNavigation);document.removeEventListener('drop',preventNavigation);},
  };
}

export function renderMessageAttachments(refs,{client,scope,locale,onError,caps}) {
  const host=node('div','message-attachments');
  for(const attachment of refs||[]){
    const image=attachmentKind(attachment.mime_type)==='image',card=node('div',image?'sent-image':'sent-document');
    const open=action('',async()=>{
      const dialog=node('dialog','attachment-viewer'),title=node('span','attachment-name',attachment.name),close=action('×',()=>dialog.close());
      close.setAttribute('aria-label',locale()==='zh-CN'?'关闭':'Close');
      const header=node('div','attachment-viewer-toolbar');header.append(title,action(locale()==='zh-CN'?'下载':'Download',()=>client.downloadAttachment(attachment,scope).catch(onError)),close);dialog.append(header);document.body.append(dialog);dialog.addEventListener('close',()=>dialog.remove(),{once:true});dialog.showModal();
      try{const url=await client.attachmentURL(attachment,scope);if(!dialog.isConnected)return;const content=node(image?'img':'iframe');if(image)content.alt=attachment.name;else content.title=attachment.name;content.src=url;dialog.append(content);}catch(reason){dialog.append(node('p','attachment-error',reason.message));}
    });
    open.className='attachment-preview control';open.setAttribute('aria-label',attachment.name);
    if(image){const img=node('img');img.alt=attachment.name;img.loading='lazy';open.append(img);client.attachmentURL(attachment,scope).then(url=>{if(img.isConnected)img.src=url;}).catch(reason=>{card.append(node('span','attachment-error',reason.message));});}
    else open.textContent=attachment.name;
    const download=action(locale()==='zh-CN'?'下载':'Download',()=>client.downloadAttachment(attachment,scope).catch(onError));
    card.append(open,node('span','attachment-size',formatAttachmentSize(attachment.size)),download);
    if(caps&&!attachmentAllowed(caps,attachment.mime_type))card.append(node('span','attachment-error',locale()==='zh-CN'?'当前模型无法读取此历史附件':'Current model cannot read this earlier attachment'));
    host.append(card);
  }
  return host;
}
