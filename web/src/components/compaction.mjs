import {preparedDOM} from './prepared-render.mjs';
import {node as n} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

export function createCompaction({locale}) {
  const element=n('div','compact-record');
  const t=key=>translate(locale(),'compact.'+key);
  return {element,update(message){
    const current=message.compaction;
    const duration=current.finished_at_ms?Math.max(0,current.finished_at_ms-message.created_at_ms):null;
    const size=current.state==='complete'?`${current.before.toLocaleString(locale())} → ${current.after.toLocaleString(locale())}`:current.before.toLocaleString(locale());
    const text=t('title')+' · '+t(current.state)+' · '+t(current.reason)+' · '+size+' tokens'+(duration==null?'':` · ${(duration/1000).toLocaleString(locale())}s`);
    preparedDOM(element,'compaction',text,target=>target.textContent=text);
  }};
}
