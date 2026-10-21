import {node as n} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

export function createCompaction({locale}) {
  const element=n('div','compact-record'),title=n('div','compact-title'),body=n('div','compact-details');
  const t=key=>translate(locale(),'compact.'+key);
  element.append(title,body);
  return {element,update(message){
    const current=message.compaction;
    title.textContent=t('title')+' · '+t(current.state);
    const duration=current.finished_at_ms?Math.max(0,current.finished_at_ms-message.created_at_ms):null;
    const size=current.state==='complete'?`${current.before.toLocaleString(locale())} → ${current.after.toLocaleString(locale())}`:current.before.toLocaleString(locale());
    body.textContent=t(current.reason)+' · '+size+' tokens'+(duration==null?'':` · ${(duration/1000).toLocaleString(locale())}s`);
  }};
}
