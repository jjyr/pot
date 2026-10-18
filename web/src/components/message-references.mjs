import {translate} from '../locales/messages.mjs';
import {icon} from './icons.mjs';

const node=(tag,cls,text)=>{const e=document.createElement(tag);e.className=cls;if(text!==undefined)e.textContent=text;return e;};
export function referenceProblem(refs) {
  const bytes=text=>new TextEncoder().encode(text).length;
  return refs.length>8||refs.some(ref=>bytes(ref.quote||'')>4096)||refs.reduce((n,ref)=>n+bytes(ref.quote||''),0)>16384;
}

export function renderMessageReferences(refs,{messages,locale,onJump,onRemove,disabled=false}) {
  const t=key=>translate(locale(),key),host=node('div','message-references');host.setAttribute('aria-label',t('references.list'));
  for(const [index,ref]of refs.entries()){
    const source=messages.find(m=>m.id===ref.message_id),card=node('div','message-reference');
    const open=node('button','message-reference-open');open.type='button';open.disabled=typeof onJump!=='function';
    const label=t(source?.role==='user'?'references.you':'references.assistant');
    const inner=node('span','message-reference-inner'),heading=node('span','message-reference-source',label),arrow=node('span','message-reference-jump','↗');heading.prepend(icon('reply'));heading.append(arrow);
    const text=ref.quote||source?.text||t('references.unavailable'),excerpt=node('span','message-reference-excerpt',text);
    open.title=text;open.setAttribute('aria-label',t('references.open').replace('{source}',label));open.onclick=()=>onJump(ref.message_id);
    inner.append(heading,excerpt);open.append(inner);card.append(open);
    if(onRemove){const remove=node('button','message-reference-remove','×');remove.type='button';remove.title=t('references.remove');remove.setAttribute('aria-label',remove.title);remove.disabled=disabled;remove.onclick=()=>onRemove(index);card.append(remove);}
    host.append(card);
  }
  return host;
}

// Capture only rendered message prose. Selection changes while dragging are not
// committed; releasing the selection preserves the native selection and focus.
export function bindMessageSelection(history,{capture}) {
  let selecting=false;
  const finish=()=>{
    const selection=window.getSelection();if(!selection||selection.isCollapsed||!selection.rangeCount)return;
    const range=selection.getRangeAt(0);
    if(!history.contains(range.startContainer)||!history.contains(range.endContainer))return;
    const refs=[];
    for(const source of history.querySelectorAll('[data-reference-source]')){
      if(!range.intersectsNode(source))continue;
      const part=document.createRange();part.selectNodeContents(source);
      if(range.compareBoundaryPoints(Range.START_TO_START,part)>0)part.setStart(range.startContainer,range.startOffset);
      if(range.compareBoundaryPoints(Range.END_TO_END,part)<0)part.setEnd(range.endContainer,range.endOffset);
      const quote=part.toString().trim();if(quote)refs.push({message_id:source.dataset.referenceSource,quote});
    }
    if(refs.length)capture(refs);
  };
  const key=event=>{if(event.key==='Shift'||event.key.startsWith('Arrow'))finish();};
  const start=event=>{selecting=!!event.target.closest?.('[data-reference-source]');};
  const end=()=>{if(selecting)finish();selecting=false;};
  document.addEventListener('pointerdown',start);document.addEventListener('pointerup',end);document.addEventListener('keyup',key);
  return ()=>{document.removeEventListener('pointerdown',start);document.removeEventListener('pointerup',end);document.removeEventListener('keyup',key);};
}
