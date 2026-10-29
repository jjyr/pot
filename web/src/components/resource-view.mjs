import {currentPreparedRender,preparedAsset,preparedIsland,deferPreparedConnection,adoptPreparedChildren} from './prepared-render.mjs';
import {moveConversationNode} from './conversation-dom.mjs';
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
    queuePreparedState(change){
      const iframe=this.querySelector('iframe'),boundedResize=change.resize!==undefined&&(this.dataset.preparedFallback==='true'||iframe?.classList.contains('prepared-frame')||iframe?.nativePreparation?.fixedHeight!==undefined);
      // A deadline viewport already has measured geometry. Pure height reports
      // cannot invalidate a result/notice transaction or reopen its preparation.
      this.resourceRenderState={...this.resourceRenderState,...change,revision:(this.resourceRenderState?.revision||0)+(boundedResize?0:1)};
      const hidden=!!this.closest('[data-preparation-surface]'),managed=hidden||this.closest('.prepared-row,.prepared-region');
      if(change.resize!==undefined&&(hidden||iframe?.nativePreparation||boundedResize))iframe?.style.setProperty('--resource-height',change.resize+'px');
      if(boundedResize)return;
      if(hidden||!managed){this.projectPreparedState(this);if(hidden){let parent=this;while(parent&&!parent.preparedContext)parent=parent.parentElement;const context=parent?.preparedContext;if(context?.alive)context.changed?.();}return;}
      if(change.resize!==undefined&&iframe?.nativePreparation&&!iframe.nativePreparation.context.ready)return;
      this.dispatchEvent(new CustomEvent('pot:prepare-resize',{bubbles:true}));
    }
    projectPreparedState(target){
      if(!target)return;const state=structuredClone(this.resourceRenderState||{revision:0});target.preparedResourceState=state;
      const iframe=target.querySelector('iframe')||target.querySelector('[data-native-preparation]');if(state.resize!==undefined)iframe?.style.setProperty('--resource-height',state.resize+'px');
      if(state.result!==undefined){const body=target.querySelector(':scope > .resource-body');let result=body.querySelector(':scope > .resource-interaction-result');if(!result){result=node('details','resource-interaction-result');result.append(node('summary','resource-result-title',translate(target.ownerDocument.documentElement.lang,'resource.result')),node('pre','resource-result-value'));body.append(result);}const pre=result.querySelector('pre');if(pre.textContent!==state.result)pre.textContent=state.result;}
      if(state.dependencyError){const notice=target.lastElementChild;notice.hidden=false;notice.textContent=translate(target.ownerDocument.documentElement.lang,'fileReference.unsupported_dependency');}
    }
    commitPreparedState(projection){
      const state=projection.preparedResourceState;if(!state||projection===this||projection.preparedResourceCommitted)return;
      if(state.revision!==(this.resourceRenderState?.revision||0))throw Error('Prepared resource state does not match its committed revision');
      const body=this.querySelector(':scope > .resource-body'),candidate=projection.querySelector(':scope > .resource-body > .resource-interaction-result'),existing=body.querySelector(':scope > .resource-interaction-result');
      if(candidate&&existing){const pre=existing.querySelector('pre'),next=candidate.querySelector('pre'),summary=existing.querySelector('summary'),nextSummary=candidate.querySelector('summary');adoptPreparedChildren(pre,next);adoptPreparedChildren(existing,candidate,{retain:[[next,pre],...(summary.isEqualNode(nextSummary)?[[nextSummary,summary]]:[])]});}
      else if(candidate)moveConversationNode(body,candidate);
      if(state.resize!==undefined)this.querySelector('iframe')?.style.setProperty('--resource-height',state.resize+'px');
      if(state.dependencyError){const notice=this.lastElementChild,copy=projection.lastElementChild;notice.hidden=copy.hidden;adoptPreparedChildren(notice,copy);}
      this.preparedResourceState=state;projection.preparedResourceCommitted=true;
    }
    connectedCallback(){
      if(this.preparationSurrogate||this.started)return;if(deferPreparedConnection(this,()=>this.connectedCallback()))return;this.started=true;
      const generation=++this.generation,{resource,mode,caption}=this.presentation,preview=resourcePreviewKind(resource),kind=this.line&&preview==='html'?'text':preview;
      const t=key=>translate(document.documentElement.lang,`resource.${key}`);
      this.dataset.kind=kind;this.dataset.mode=mode;
      if(kind==='image')this.classList.add('is-expanded');
      const head=node('div','resource-heading'),identity=node('div','resource-identity'),badge=node('span','resource-type'),title=node('strong','resource-name',caption||resource.name),details=node('span','resource-details');
      const actions=node('div','resource-actions'),download=node('button','resource-action'),expand=node('button','resource-action'),status=node('p','resource-status'),saveStatus=node('p','resource-status resource-download-error'),body=node('div','resource-body'),notice=node('p','resource-status');
      download.classList.add('busy-control');download.type=expand.type='button';status.setAttribute('role','status');status.hidden=true;saveStatus.setAttribute('role','alert');saveStatus.hidden=true;identity.append(title,details);head.append(badge,identity,actions);actions.append(expand,download);notice.hidden=!this.noticeKey;this.replaceChildren(head,status,saveStatus,body,notice);
      this.lease=this.acquireResource?.(resource);const lease=this.lease;
      let error=false;
      this.stopRetire=lease?.onRetire?.(()=>{this.retireRendering();body.replaceChildren();error=true;this.dataset.state='error';this.removeAttribute('aria-busy');status.hidden=false;refresh();});
      const current=()=>this.isConnected&&generation===this.generation;
      const setURL=(element,url)=>Promise.resolve(url).then(value=>{if(current())element.src=value;}).catch(fail);
      const refresh=()=>{const size=resource.size,unit=size<1024?'byte':size<1024*1024?'kilobyte':'megabyte',divisor=size<1024?1:size<1024*1024?1024:1024*1024;details.textContent=`${resource.name} · ${new Intl.NumberFormat(document.documentElement.lang,{style:'unit',unit,unitDisplay:'narrow',maximumFractionDigits:1}).format(size/divisor)}`;badge.textContent=t(mode==='download'?'file':kind);download.textContent=t(resource.files.length>1?'downloadBundle':'download');download.setAttribute('aria-label',t('downloadName').replace('{name}',resource.download?.path.split('/').at(-1)||resource.name));expand.textContent=kind==='image'?translate(document.documentElement.lang,'markdown.imagePreview'):t(this.classList.contains('is-expanded')?'collapse':'expand');status.textContent=error?t('unavailable'):'';saveStatus.textContent=t('downloadFailed');if(this.noticeKey)notice.textContent=translate(document.documentElement.lang,this.noticeKey);const resultTitle=this.querySelector('.resource-result-title');if(resultTitle)resultTitle.textContent=t('result');};
      const fail=()=>{if(!current())return;error=true;this.dataset.state='error';this.removeAttribute('aria-busy');status.hidden=false;refresh();};
      const ready=()=>{if(!current())return;this.dataset.state='ready';this.removeAttribute('aria-busy');status.hidden=true;};
      refresh();window.addEventListener('pot:locale',refresh);this.stopLocale=()=>window.removeEventListener('pot:locale',refresh);
      download.onclick=async()=>{download.disabled=true;download.setAttribute('aria-busy','true');saveStatus.hidden=true;try{await this.downloadResource(resource);}catch{if(current()){saveStatus.textContent=t('downloadFailed');saveStatus.hidden=false;}}finally{if(current()){download.disabled=false;download.removeAttribute('aria-busy');}}};
      expand.onclick=()=>{this.classList.toggle('is-expanded');refresh();this.dispatchEvent(new CustomEvent('pot:prepare-resize',{bubbles:true}));};
      expand.hidden=kind==='audio';
      if(mode==='download'||kind==='file'){body.hidden=expand.hidden=status.hidden=true;this.dataset.state='ready';this.removeAttribute('aria-busy');return;}
      if(!lease){fail();return;}
      this.dataset.state='loading';this.setAttribute('aria-busy','true');
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
        const frame=node('iframe','resource-document');frame.title=caption||resource.name;frame.onerror=fail;
        // An attached iframe without its requested URL can load about:blank.
        // That event is not PDF readiness while the authenticated URL waits.
        Promise.resolve(lease.url(resource.entry)).then(url=>{if(current()){frame.onload=ready;frame.src=url;body.append(frame);}}).catch(fail);
      }else if(kind==='markdown'){
        const prose=node('div','resource-markdown message-body'),pre=node('pre','resource-text'),toggle=node('button','resource-action');
        toggle.type='button';toggle.disabled=true;pre.hidden=true;body.append(prose,pre);actions.prepend(toggle);
        let source=!!this.line;prose.hidden=source;pre.hidden=!source;
        const refreshToggle=()=>{toggle.textContent=t(source?'showMarkdown':'showSource');toggle.setAttribute('aria-pressed',String(source));};
        toggle.onclick=()=>{source=!source;prose.hidden=source;pre.hidden=!source;refreshToggle();this.dispatchEvent(new CustomEvent('pot:prepare-resize',{bubbles:true}));};
        refreshToggle();window.addEventListener('pot:locale',refreshToggle);const stop=this.stopLocale;this.stopLocale=()=>{stop();window.removeEventListener('pot:locale',refreshToggle);};
        lease.read(resource.entry).then(blob=>blob.text()).then(text=>{if(current()){
          // No workspace image/download adapters: raw HTML stays literal, remote
          // images stay links, and undeclared relative paths cannot fetch bytes.
          sourceText(pre,text,this.line);prose.append(renderMarkdown(text,{markdownSlot:'resource:'+resource.id}));toggle.disabled=false;ready();
        }}).catch(fail);
      }else if(kind==='text'){
        const pre=node('pre','resource-text');body.append(pre);lease.read(resource.entry).then(blob=>blob.text()).then(text=>{if(current()){sourceText(pre,text,this.line);ready();}}).catch(fail);
      }else if(kind==='html'){
        const frame=node('iframe','resource-interactive');frame.title=caption||resource.name;frame.setAttribute('sandbox','allow-scripts');frame.setAttribute('allow',"camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'");frame.referrerPolicy='no-referrer';
        const token=crypto.randomUUID(),factoryToken=crypto.randomUUID();let displayDocument,factoryStarted=false;
        const onMessage=event=>{
          if(!current()||event.source!==frame.contentWindow)return;
          if(lease.renderDocument&&event.data?.type==='pot-resource-factory'&&event.origin===location.origin&&event.data.token===factoryToken){
            if(event.data.action==='ready'&&displayDocument&&!factoryStarted){factoryStarted=true;frame.setAttribute('sandbox','allow-scripts');frame.contentWindow.postMessage({type:'pot-resource-document',token:factoryToken,html:displayDocument},'*');displayDocument=null;}
            if(event.data.action==='url'&&factoryStarted&&typeof event.data.url==='string'&&event.data.url.startsWith('blob:'))lease.trackDocumentURL(event.data.url);
            return;
          }
          if(event.data?.type!=='pot-resource'||event.data.token!==token)return;
          if(event.data.action==='ready')ready();
          if(event.data.action==='escape')this.closest('dialog')?.close();
          if(event.data.action==='dependency_error')this.queuePreparedState({dependencyError:true});
          if(event.data.action==='resize'&&Number.isFinite(event.data.value)){this.nativeResizeRevision=(this.nativeResizeRevision||0)+1;this.queuePreparedState({resize:Math.min(960,Math.max(180,event.data.value))});}
          if(event.data.action==='result'){
            let value;try{value=JSON.stringify(event.data.value);}catch{return;}if(!value||value.length>16384)return;
            this.queuePreparedState({result:value});
          }
        };
        window.addEventListener('message',onMessage);this.stopMessages=()=>window.removeEventListener('message',onMessage);
        frame.onerror=fail;frame.onload=()=>{if(lease.renderDocument&&!factoryStarted)return;this.loadTimer=setTimeout(()=>{if(current()&&this.dataset.state==='loading')fail();},250);};if(lease.renderDocument)lease.renderDocument(token).then(html=>{if(current()){displayDocument=html;frame.setAttribute('sandbox','allow-scripts allow-same-origin');frame.src='/resource-document.html#'+factoryToken;body.append(frame);}}).catch(fail);else Promise.resolve(lease.renderURL(token)).then(url=>{if(current()){frame.src=url;body.append(frame);}}).catch(fail);
      }
    }
    disconnectedCallback(){queueMicrotask(()=>{if(!this.isConnected)this.retireRendering();});}
    retireRendering(){
      this.preparationTicket?.release();
      if(!this.started)return;this.started=false;
      ++this.generation;this.viewer?.close();this.stopRetire?.();this.stopLocale?.();this.stopMessages?.();clearTimeout(this.loadTimer);
      delete this.nativeReflowDeadline;
      for(const media of this.querySelectorAll('audio,video')){media.pause();media.removeAttribute('src');media.load();}
      for(const image of this.querySelectorAll('img'))image.removeAttribute('src');
      for(const frame of this.querySelectorAll('iframe')){frame.nativePresentation?.cancel();frame.removeAttribute('src');frame.srcdoc='';}
      this.lease?.release();this.lease=null;
    }
  });
}

export function createResourceView(presentation,options={}){
  register();const slot=options.renderSlot||'resource',source=JSON.stringify([presentation,options.line,options.noticeKey]);
  const element=preparedAsset(slot,source,()=>{const value=document.createElement('pot-resource');Object.assign(value,{presentation,...options,renderSlot:slot});return preparedIsland(value);});
  return currentPreparedRender()?.context.phase==='prepare'?element:element.preparationOriginal||element;
}
