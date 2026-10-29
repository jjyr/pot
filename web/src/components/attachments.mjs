import {uploadChatOriginal} from '../state/chat-upload.mjs';
import {icon} from './icons.mjs';
import {translate} from '../locales/messages.mjs';
import {createResourceView} from './resource-view.mjs';

export const attachmentLimit = 8 * 1024 * 1024;
export const attachmentTypes = {image:['image/png','image/jpeg','image/webp'],pdf:['application/pdf']};
export function attachmentKind(mime) {return attachmentTypes.image.includes(mime)?'image':mime==='application/pdf'?'pdf':null;}
export function attachmentAllowed(caps,mime) {return !!caps?.[attachmentKind(mime)];}
export function attachmentProblem(items,caps,zh=false) {
  const t=key=>translate(zh?'zh-CN':'en',key);
  if(items.length>8||items.reduce((size,item)=>size+item.attachment.size,0)>attachmentLimit)return t('attachments.restoredLimit');
  for(const item of items) {
    if(caps&&!attachmentAllowed(caps,item.attachment.mime_type))return t(attachmentKind(item.attachment.mime_type)==='image'?'attachments.imagesUnsupported':'attachments.typeUnsupported');
    if(item.phase!=='ready')return t(item.phase==='failed'?'attachments.failed':item.phase==='waiting'?'attachments.waiting':'attachments.uploading');
    if(!caps)return t('attachments.waiting');
  }
  return '';
}
export function failedMessageInput(snapshot) {
  if(!snapshot||snapshot.running)return null;
  const messages=snapshot.messages||[],anchor=messages.findLast(m=>m.role==='user'&&!m.steering);
  if(anchor?.status!=='failed')return null;
  const input=messages.slice(messages.indexOf(anchor)).findLast(m=>m.role==='user'&&(m.attachments?.length||m.references?.length));
  return input?{input,target:anchor.id}:null;
}
export function formatAttachmentSize(size) {return size<1024?`${size} B`:size<1024*1024?`${(size/1024).toFixed(1)} KB`:`${(size/1024/1024).toFixed(1)} MB`;}
const node=(tag,cls='',text)=>{const e=document.createElement(tag);e.className=cls;if(text!==undefined)e.textContent=text;return e;};
const action=(label,run)=>{const b=node('button','control',label);b.type='button';b.onclick=run;return b;};
const mimeOf=file=>file.type||({'png':'image/png','jpg':'image/jpeg','jpeg':'image/jpeg','webp':'image/webp','pdf':'application/pdf'}[file.name.split('.').at(-1).toLowerCase()]||'');

// One shared byte-upload UI for DOM File objects in browsers and Wails WebViews.
// Every asynchronous operation captures its owner/Space before starting.
export function createAttachmentComposer({client,form,input,getKey,getCaps,canSelect,canUpload,changed,draftChanged=()=>{},locale}) {
  const drafts=new Map(),notices=new Map(),urls=new Set();let disposed=false,signature='',intent=null;
  const dropTarget=form.closest('.workspace')||form;
  const strip=node('div','composer-attachments'),hint=node('div','attachment-hint'),toolbar=node('div','composer-toolbar'),menu=node('div','attachment-menu');
  hint.setAttribute('role','status');menu.hidden=true;
  const plus=action('',()=>{menu.hidden=!menu.hidden;});plus.className='attachment-plus control';plus.append(icon('plus'));plus.setAttribute('aria-haspopup','menu');
  const plusHost=node('span','attachment-trigger'),tooltip=node('span','attachment-tooltip');
  tooltip.id=`attachment-tooltip-${crypto.randomUUID()}`;tooltip.setAttribute('role','tooltip');tooltip.hidden=true;
  plusHost.append(plus,tooltip);plusHost.setAttribute('role','group');
  const pickImage=action('',()=>pick('image')),pickPDF=action('',()=>pick('pdf'));menu.append(pickImage,pickPDF);menu.setAttribute('role','menu');
  for(const b of [pickImage,pickPDF])b.setAttribute('role','menuitem');
  toolbar.append(plusHost,menu);form.prepend(strip);form.append(hint,toolbar);
  const list=(key=getKey())=>drafts.get(key)||[];
  const refresh=()=>{signature='';if(!disposed)changed();};
  const set=(key,items)=>{drafts.set(key,items);draftChanged(key);refresh();};
  const zh=()=>locale()==='zh-CN';
  const t=key=>translate(locale(),key);
  const context=()=>({key:getKey(),scope:{...client.scope()}});
  const current=selection=>!disposed&&selection.key===getKey()&&JSON.stringify(selection.scope)===JSON.stringify(client.scope());
  function cancelPicker(notify=false){
    if(!intent)return;
    intent.cancelled=true;intent.picker.remove();intent=null;
    if(notify)notices.set(getKey(),'attachments.selectionCancelled');
    signature='';
  }
  function pick(kind){
    if(!canSelect())return;
    cancelPicker();
    // A fresh input gives every chooser its own intent. A late change on an old
    // native chooser can never be mistaken for a later selection.
    const selection={...context(),cancelled:false},picker=node('input');
    selection.picker=picker;intent=selection;
    picker.type='file';picker.multiple=true;picker.hidden=true;picker.accept=attachmentTypes[kind].join(',');
    picker.addEventListener('cancel',()=>{if(intent===selection)cancelPicker();});
    picker.addEventListener('change',()=>{
      const files=[...picker.files];picker.remove();
      if(selection.cancelled||intent!==selection||!current(selection)){if(!disposed){notices.set(getKey(),'attachments.selectionCancelled');refresh();}return;}
      intent=null;add(files,selection);
    },{once:true});
    toolbar.append(picker);menu.hidden=true;picker.click();
  }
  const live=(item,key)=>!disposed&&!item.removed&&list(key).includes(item);
  function remove(item,key){
    cancelPicker(true);
    item.removed=true;item.abort?.abort();set(key,list(key).filter(a=>a!==item));
    if(item.url){URL.revokeObjectURL(item.url);urls.delete(item.url);}
    // Submitted originals still belong to an immutable pending send snapshot.
    // Keep them on Control; normal unreferenced-file expiry handles abandonment.
    if(item.started&&!item.submitted)Promise.resolve(item.pending).then(()=>client.request('/api/v1/attachments',{...item.scope,method:'POST',body:{action:'remove',id:item.attachment.id}})).catch(()=>{});
  }
  async function upload(item,key){
    if(!live(item,key)||item.pending||key!==getKey()||!canUpload()||!attachmentAllowed(getCaps(),item.attachment.mime_type))return;
    if(!item.file)return;
    item.phase='uploading';item.error='';item.abort=new AbortController();item.started=true;draftChanged(key);
    const check=()=>{if(!live(item,key)||item.abort.signal.aborted)throw new DOMException('Cancelled','AbortError');};
    item.pending=(async()=>{
      const attachment=await uploadChatOriginal(client,item.attachment,item.file,item.scope,{signal:item.abort.signal,check,progress:value=>{item.progress=value;refresh();}});
      item.attachment=attachment;item.phase='ready';item.progress=1;
    })().catch(reason=>{if(live(item,key)){item.phase='failed';item.error=reason.message||String(reason);}}).finally(()=>{item.pending=null;if(live(item,key))draftChanged(key);refresh();});
    refresh();await item.pending;
  }
  function add(files,selection=context()){
    if(!selection.key||!current(selection)){if(!disposed){notices.set(getKey(),'attachments.selectionCancelled');refresh();}return;}
    const {key,scope}=selection,items=[...list(key)];let rejection='';
    for(const file of files){
      const mime=mimeOf(file);
      if(!attachmentKind(mime)){rejection='attachments.typeUnsupported';continue;}
      if(!file.size||items.length>=8||items.reduce((n,a)=>n+a.attachment.size,0)+file.size>attachmentLimit){rejection='attachments.limit';continue;}
      const item={attachment:{id:crypto.randomUUID(),name:file.name,mime_type:mime,size:file.size},file,scope,phase:'waiting',progress:0};
      if(attachmentKind(mime)==='image'){item.url=URL.createObjectURL(file);urls.add(item.url);}
      items.push(item);
    }
    if(rejection)notices.set(key,rejection);else notices.delete(key);
    // Store every File before validation/upload can trigger a render.
    set(key,items);
  }
  const drag=event=>{if([...event.dataTransfer?.types||[]].includes('Files')){event.preventDefault();form.classList.add('attachment-drag');}};
  const leave=event=>{if(!dropTarget.contains(event.relatedTarget))form.classList.remove('attachment-drag');};
  const drop=event=>{if(event.dataTransfer?.files?.length){event.preventDefault();form.classList.remove('attachment-drag');add([...event.dataTransfer.files]);}};
  const paste=event=>{const files=[...event.clipboardData?.files||[]];if(files.length){event.preventDefault();add(files);}};
  dropTarget.addEventListener('dragover',drag);dropTarget.addEventListener('dragleave',leave);dropTarget.addEventListener('drop',drop);input.addEventListener('paste',paste);
  const preventNavigation=event=>{if([...event.dataTransfer?.types||[]].includes('Files'))event.preventDefault();};
  document.addEventListener('dragover',preventNavigation);document.addEventListener('drop',preventNavigation);
  return {
    toolbar,
    items:list,
    refs:()=>list().map(item=>item.attachment),
    localProblem:()=>{const items=list();return items.length>8||items.reduce((n,item)=>n+item.attachment.size,0)>attachmentLimit?t('attachments.restoredLimit'):'';},
    problem:()=>attachmentProblem(list(),getCaps(),zh())||(list().length&&!canUpload()?t('attachments.waiting'):''),
    cancelSelection(){cancelPicker(true);refresh();},
    markSubmitted(key,refs){const ids=new Set(refs.map(a=>a.id)),previous=list(key).filter(item=>ids.has(item.attachment.id)).map(item=>[item,item.submitted]);for(const [item]of previous)item.submitted=true;draftChanged(key);return()=>{for(const [item,submitted]of previous)if(list(key).includes(item))item.submitted=submitted;draftChanged(key);};},
    clearSubmitted(key,refs){const ids=new Set(refs.map(a=>a.id));for(const item of list(key))if(ids.has(item.attachment.id)){item.submitted=true;item.removed=true;item.abort?.abort();if(item.url){URL.revokeObjectURL(item.url);urls.delete(item.url);}}set(key,list(key).filter(item=>!ids.has(item.attachment.id)));},
    restore(key,refs,scope){if(list(key).length)return;set(key,refs.map(attachment=>({attachment,scope,phase:'ready',progress:1})));},
    hydrate(key,records,files,scope){
      // Paste/drop may already have accepted Files while the disk read (or its
      // retry) was pending. Keep those exact live items, including upload state.
      // Retain every saved original too; an oversized merge blocks Send until
      // the user chooses what to remove instead of silently discarding files.
      const local=list(key),ids=new Set(local.map(item=>item.attachment.id));
      const originals=new Map(files.map(file=>[file.id,file]));
      const items=records.filter(record=>!ids.has(record.attachment.id)).map(record=>{
        const original=originals.get(record.attachment.id),file=original&&new File([original.blob],record.attachment.name,{type:record.attachment.mime_type,lastModified:original.lastModified});
        const item={...record,scope:{...scope},file,phase:file&&record.phase==='ready'?'waiting':record.phase,progress:0};
        if(file&&attachmentKind(item.attachment.mime_type)==='image'){item.url=URL.createObjectURL(file);urls.add(item.url);}
        return item;
      });
      set(key,[...items,...local]);
    },
    discard(key){for(const item of list(key)){item.removed=true;item.abort?.abort();if(item.url){URL.revokeObjectURL(item.url);urls.delete(item.url);}}drafts.delete(key);notices.delete(key);},
    forget(scope){
      if(intent?.scope.space===scope.space&&intent?.scope.owner===scope.owner)cancelPicker(true);
      for(const [key,items]of drafts){if(items.some(item=>item.scope.space===scope.space&&item.scope.owner===scope.owner)){for(const item of [...items])remove(item,key);drafts.delete(key);notices.delete(key);}}
      refresh();
    },
    matches(refs){return JSON.stringify(this.refs())===JSON.stringify(refs||[]);},
    update(){
      if(disposed)return;
      if(intent&&(!current(intent)||!canSelect()))cancelPicker(true);
      const caps=getCaps(),items=list(),key=getKey();plus.setAttribute('aria-label',t('attachments.add'));
      plus.disabled=!canSelect()||!!caps&&!(caps.image||caps.pdf);if(plus.disabled)menu.hidden=true;
      const reason=caps&&!(caps.image||caps.pdf)?t('attachments.modelUnsupported'):!canSelect()?t('attachments.unavailable'):'';
      plus.title=reason||t('attachments.add');tooltip.textContent=reason;tooltip.hidden=!reason;
      plusHost.tabIndex=plus.disabled?0:-1;plusHost.setAttribute('aria-label',t('attachments.add'));
      for(const element of [plusHost,plus]){if(reason)element.setAttribute('aria-describedby',tooltip.id);else element.removeAttribute('aria-describedby');}
      pickImage.textContent=t('attachments.images');pickPDF.textContent=t('attachments.pdf');pickImage.disabled=!!caps&&!caps.image;pickPDF.disabled=!!caps&&!caps.pdf;
      pickImage.title=!caps||caps.image?'':t('attachments.imagesUnsupported');pickPDF.title=!caps||caps.pdf?'':t('attachments.pdfUnsupported');
      // One bounded attempt per waiting item. Failures require an explicit retry;
      // capability/connection changes only advance files not yet attempted.
      for(const item of items)if(item.phase==='waiting'&&canUpload()&&attachmentAllowed(caps,item.attachment.mime_type))queueMicrotask(()=>upload(item,key));
      const problem=this.problem(),next=JSON.stringify([key,items.map(i=>[i.attachment.id,i.phase,i.progress,i.error]),caps,zh(),notices.get(key),problem,canUpload()]);
      if(next===signature)return;signature=next;strip.replaceChildren();strip.hidden=!items.length;
      hint.textContent=[notices.has(key)?t(notices.get(key)):'',problem].filter(Boolean).join(' ');hint.hidden=!hint.textContent;
      for(const item of items){
        const card=node('div',`attachment-card ${attachmentKind(item.attachment.mime_type)==='image'?'is-image':''}`);
        if(attachmentKind(item.attachment.mime_type)==='image'){
          const image=node('img');image.alt=item.attachment.name;card.append(image);
          if(item.url)image.src=item.url;
          else client.attachmentURL(item.attachment,item.scope).then(url=>{if(image.isConnected)image.src=url;}).catch(()=>{});
        }
        const info=node('div','attachment-info');info.append(node('span','attachment-name',item.attachment.name),node('span','attachment-size',formatAttachmentSize(item.attachment.size)));card.append(info);
        const close=action('×',()=>remove(item,key));close.className='attachment-remove control';close.setAttribute('aria-label',t('attachments.remove'));card.append(close);
        if(item.phase==='waiting')card.append(node('span','attachment-error',t('attachments.waiting')));
        if(item.phase==='uploading'){const progress=node('progress');progress.max=1;progress.value=item.progress;progress.setAttribute('aria-label',t('attachments.progress'));card.append(progress);}
        if(item.phase==='failed'){
          card.classList.add('has-error');card.append(node('span','attachment-error',t('attachments.failed')));
          const details=node('details','attachment-error');details.append(node('summary','',t('web.error.details')),node('span','',item.error));card.append(details);
          const retry=action(t('retry'),()=>{item.phase='waiting';refresh();});retry.disabled=!canUpload()||!attachmentAllowed(caps,item.attachment.mime_type);card.append(retry);
        }
        if(caps&&!attachmentAllowed(caps,item.attachment.mime_type))card.classList.add('is-incompatible');
        strip.append(card);
      }
    },
    destroy(){disposed=true;cancelPicker();for(const items of drafts.values())for(const item of items){item.removed=true;item.abort?.abort();}for(const url of urls)URL.revokeObjectURL(url);drafts.clear();dropTarget.removeEventListener('dragover',drag);dropTarget.removeEventListener('dragleave',leave);dropTarget.removeEventListener('drop',drop);input.removeEventListener('paste',paste);document.removeEventListener('dragover',preventNavigation);document.removeEventListener('drop',preventNavigation);},
  };
}

export function renderMessageAttachments(refs,{client,scope,locale,onError,caps,originalResource}) {
  const host=node('div','message-attachments');host.dispose=()=>host.replaceChildren();
  for(const attachment of refs||[]){
    const resource={...attachment,entry:attachment.name,files:[],download:{path:attachment.name}};
    const card=createResourceView({resource,mode:'preview'},{
      renderSlot:'attachment:'+attachment.id,
      acquireResource:()=>{if(originalResource)return originalResource(attachment);const release=client.retainAttachment?.(attachment,scope);return {...(client.attachmentBlob?{read:()=>client.attachmentBlob(attachment,scope)}:{}),url:()=>client.attachmentURL(attachment,scope),release:()=>release?.(),onRetire:listener=>client.onResourceRetire?.(scope,listener)};},
      downloadResource:()=>client.downloadAttachment(attachment,scope),
      noticeKey:caps&&!attachmentAllowed(caps,attachment.mime_type)?'attachments.historyUnavailable':null,
    });
    host.append(card);
  }
  return host;
}
