import {translate} from '../locales/messages.mjs';
import {renderMarkdown} from './markdown.mjs';
import {openImageViewer} from './image-viewer.mjs';

const node=(tag,cls='',text)=>{const element=document.createElement(tag);element.className=cls;if(text!==undefined)element.textContent=text;return element;};
function sourceText(pre,text,line){
  pre.textContent=text;if(!line)return;const lines=text.split('\n'),index=Math.min(lines.length-1,Math.max(0,line-1)),before=lines.slice(0,index).join('\n');
  const target=document.createElement('mark');target.textContent=lines[index];pre.replaceChildren(document.createTextNode(before+(index?'\n':'')),target,document.createTextNode((index<lines.length-1?'\n':'')+lines.slice(index+1).join('\n')));requestAnimationFrame(()=>{pre.scrollTop=target.offsetTop-pre.offsetTop;});
}
const previewKind=type=>/^image\/(png|jpeg|webp|gif)$/.test(type)?'image':/^audio\/(mpeg|aac|wav|wave|x-wav|ogg|mp4|webm|flac)$/.test(type)?'audio':/^video\/(mp4|webm|ogg)$/.test(type)?'video':type==='application/pdf'?'pdf':type==='text/html'?'html':type.startsWith('text/')?'text':'file';

// Entry is the immutable source path. Display labels and download basenames do
// not select a renderer. Retain old text/plain manifests without rewriting them.
export function resourcePreviewKind(resource){
  const type=String(resource.mime_type||'').split(';')[0].trim().toLowerCase();
  if(['text/markdown','text/x-markdown'].includes(type)||['text/plain','text/html'].includes(type)&&/\.(md|markdown)$/i.test(resource.entry||''))return 'markdown';
  return previewKind(type);
}

function register(){
  if(customElements.get('pot-resource'))return;
  customElements.define('pot-resource',class extends HTMLElement{
    generation=0;
    connectedCallback(){
      const generation=++this.generation,{resource,mode,caption}=this.presentation,preview=resourcePreviewKind(resource),kind=this.line&&preview==='html'?'text':preview;
      const t=key=>translate(document.documentElement.lang,`resource.${key}`);
      this.dataset.kind=kind;this.dataset.mode=mode;
      if(kind==='image')this.classList.add('is-expanded');
      const head=node('div','resource-heading'),identity=node('div','resource-identity'),badge=node('span','resource-type'),title=node('strong','resource-name',caption||resource.name),details=node('span','resource-details');
      const actions=node('div','resource-actions'),download=node('button','resource-action'),expand=node('button','resource-action'),status=node('p','resource-status'),saveStatus=node('p','resource-status resource-download-error'),body=node('div','resource-body'),notice=node('p','resource-status');
      download.type=expand.type='button';status.setAttribute('role','status');saveStatus.setAttribute('role','alert');saveStatus.hidden=true;identity.append(title,details);head.append(badge,identity,actions);actions.append(expand,download);notice.hidden=!this.noticeKey;this.replaceChildren(head,status,saveStatus,body,notice);
      this.lease=this.acquireResource?.(resource);const lease=this.lease;
      let error=false;
      const current=()=>this.isConnected&&generation===this.generation;
      const setURL=(element,url)=>Promise.resolve(url).then(value=>{if(current())element.src=value;}).catch(fail);
      const refresh=()=>{const size=resource.size,unit=size<1024?'byte':size<1024*1024?'kilobyte':'megabyte',divisor=size<1024?1:size<1024*1024?1024:1024*1024;details.textContent=`${resource.name} · ${new Intl.NumberFormat(document.documentElement.lang,{style:'unit',unit,unitDisplay:'narrow',maximumFractionDigits:1}).format(size/divisor)}`;badge.textContent=t(mode==='download'?'file':kind);download.textContent=t(resource.files.length>1?'downloadBundle':'download');download.setAttribute('aria-label',t('downloadName').replace('{name}',resource.download?.path.split('/').at(-1)||resource.name));expand.textContent=kind==='image'?translate(document.documentElement.lang,'markdown.imagePreview'):t(this.classList.contains('is-expanded')?'collapse':'expand');status.textContent=t(error?'unavailable':'loading');saveStatus.textContent=t('downloadFailed');if(this.noticeKey)notice.textContent=translate(document.documentElement.lang,this.noticeKey);const resultTitle=this.querySelector('.resource-result-title');if(resultTitle)resultTitle.textContent=t('result');};
      const fail=()=>{if(!current())return;error=true;this.dataset.state='error';status.hidden=false;refresh();};
      const ready=()=>{if(!current())return;this.dataset.state='ready';status.hidden=true;};
      refresh();window.addEventListener('pot:locale',refresh);this.stopLocale=()=>window.removeEventListener('pot:locale',refresh);
      download.onclick=async()=>{download.disabled=true;saveStatus.hidden=true;try{await this.downloadResource(resource);}catch{if(current()){saveStatus.textContent=t('downloadFailed');saveStatus.hidden=false;}}finally{if(current())download.disabled=false;}};
      expand.onclick=()=>{this.classList.toggle('is-expanded');refresh();};
      expand.hidden=kind==='audio';
      if(mode==='download'||kind==='file'){body.hidden=expand.hidden=status.hidden=true;this.dataset.state='ready';return;}
      if(!lease){fail();return;}
      this.dataset.state='loading';
      if(kind==='image'){
        const image=node('img','resource-image');image.alt=caption||resource.name;image.loading='eager';image.decoding='async';image.hidden=true;image.onload=async()=>{try{await image.decode();if(current()){image.hidden=false;ready();}}catch{fail();}};image.onerror=fail;setURL(image,lease.url(resource.entry));body.append(image);
        expand.disabled=true;
        expand.setAttribute('aria-haspopup','dialog');image.setAttribute('aria-haspopup','dialog');
        const loaded=image.onload;image.onload=async()=>{await loaded();if(current())expand.disabled=this.dataset.state!=='ready';};
        const open=trigger=>{if(this.viewer||this.dataset.state!=='ready')return;this.viewer=openImageViewer({image,label:caption||resource.name,trigger,download:()=>this.downloadResource(resource),onClose:()=>{this.viewer=null;}});};
        expand.onclick=()=>open(expand);image.onclick=()=>open(image);image.tabIndex=0;image.setAttribute('role','button');image.onkeydown=event=>{if(['Enter',' '].includes(event.key)){event.preventDefault();open(image);}};
        if(resource.mime_type==='image/gif'){
          const pause=node('button','resource-action');pause.type='button';pause.disabled=true;actions.prepend(pause);let paused=false;
          const toggle=()=>{paused=!paused;if(paused&&image.complete){const canvas=node('canvas');canvas.width=image.naturalWidth;canvas.height=image.naturalHeight;canvas.getContext('2d').drawImage(image,0,0);canvas.className='resource-image resource-gif-still';canvas.tabIndex=0;canvas.setAttribute('role','button');canvas.setAttribute('aria-label',caption||resource.name);canvas.setAttribute('aria-haspopup','dialog');canvas.onclick=()=>open(canvas);canvas.onkeydown=event=>{if(['Enter',' '].includes(event.key)){event.preventDefault();open(canvas);}};body.prepend(canvas);image.hidden=true;}else{body.querySelector('canvas')?.remove();image.hidden=false;}pause.textContent=t(paused?'resume':'pause');};
          pause.textContent=t('pause');pause.onclick=toggle;const prior=image.onload;image.onload=async()=>{if(!current())return;await prior();if(!current()||this.dataset.state!=='ready')return;pause.disabled=false;if(matchMedia('(prefers-reduced-motion: reduce)').matches&&!paused)toggle();};
          const oldRefresh=refresh;const updatePause=()=>{oldRefresh();pause.textContent=t(paused?'resume':'pause');};window.addEventListener('pot:locale',updatePause);const stop=this.stopLocale;this.stopLocale=()=>{stop();window.removeEventListener('pot:locale',updatePause);};
        }
      }else if(kind==='audio'||kind==='video'){
        const media=node(kind,'resource-media');media.controls=true;media.preload='metadata';media.setAttribute('aria-label',caption||resource.name);if(kind==='video')media.playsInline=true;media.onloadedmetadata=ready;media.onerror=fail;setURL(media,lease.url(resource.entry));body.append(media);
      }else if(kind==='pdf'){
        const frame=node('iframe','resource-document');frame.title=caption||resource.name;frame.onload=ready;frame.onerror=fail;setURL(frame,lease.url(resource.entry));body.append(frame);
      }else if(kind==='markdown'){
        const prose=node('div','resource-markdown message-body'),pre=node('pre','resource-text'),toggle=node('button','resource-action');
        toggle.type='button';toggle.disabled=true;pre.hidden=true;body.append(prose,pre);actions.prepend(toggle);
        let source=!!this.line;prose.hidden=source;pre.hidden=!source;
        const refreshToggle=()=>{toggle.textContent=t(source?'showMarkdown':'showSource');toggle.setAttribute('aria-pressed',String(source));};
        toggle.onclick=()=>{source=!source;prose.hidden=source;pre.hidden=!source;refreshToggle();};
        refreshToggle();window.addEventListener('pot:locale',refreshToggle);const stop=this.stopLocale;this.stopLocale=()=>{stop();window.removeEventListener('pot:locale',refreshToggle);};
        lease.read(resource.entry).then(blob=>blob.text()).then(text=>{if(current()){
          // No workspace image/download adapters: raw HTML stays literal, remote
          // images stay links, and undeclared relative paths cannot fetch bytes.
          sourceText(pre,text,this.line);prose.append(renderMarkdown(text));toggle.disabled=false;ready();
        }}).catch(fail);
      }else if(kind==='text'){
        const pre=node('pre','resource-text');body.append(pre);lease.read(resource.entry).then(blob=>blob.text()).then(text=>{if(current()){sourceText(pre,text,this.line);ready();}}).catch(fail);
      }else if(kind==='html'){
        const frame=node('iframe','resource-interactive');frame.title=caption||resource.name;frame.setAttribute('sandbox','allow-scripts');frame.setAttribute('allow',"camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'");frame.referrerPolicy='no-referrer';body.append(frame);
        const token=crypto.randomUUID();
        const onMessage=event=>{
          if(!current()||event.source!==frame.contentWindow||event.data?.type!=='pot-resource'||event.data.token!==token)return;
          if(event.data.action==='ready')ready();
          if(event.data.action==='escape')this.closest('dialog')?.close();
          if(event.data.action==='dependency_error'){this.noticeKey='fileReference.unsupported_dependency';notice.hidden=false;refresh();}
          if(event.data.action==='resize'&&Number.isFinite(event.data.value))frame.style.setProperty('--resource-height',`${Math.min(960,Math.max(180,event.data.value))}px`);
          if(event.data.action==='result'){
            let value;try{value=JSON.stringify(event.data.value);}catch{return;}if(!value||value.length>16384)return;
            let result=body.querySelector('.resource-interaction-result');if(!result){result=node('details','resource-interaction-result');result.append(node('summary','resource-result-title',t('result')),node('pre','resource-result-value'));body.append(result);}result.querySelector('pre').textContent=value;
          }
        };
        window.addEventListener('message',onMessage);this.stopMessages=()=>window.removeEventListener('message',onMessage);
        frame.onerror=fail;frame.onload=()=>{this.loadTimer=setTimeout(()=>{if(current()&&this.dataset.state==='loading')fail();},250);};frame.src=lease.renderURL(token);
      }
    }
    disconnectedCallback(){
      ++this.generation;this.viewer?.close();this.stopLocale?.();this.stopMessages?.();clearTimeout(this.loadTimer);
      for(const media of this.querySelectorAll('audio,video')){media.pause();media.removeAttribute('src');media.load();}
      for(const image of this.querySelectorAll('img'))image.removeAttribute('src');
      for(const frame of this.querySelectorAll('iframe')){frame.removeAttribute('src');frame.srcdoc='';}
      this.lease?.release();this.lease=null;
    }
  });
}

export function createResourceView(presentation,options={}){
  register();const element=document.createElement('pot-resource');Object.assign(element,{presentation,...options});return element;
}
