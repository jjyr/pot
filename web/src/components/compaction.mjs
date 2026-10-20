import {node as n,action} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

export async function downloadCompaction(request,scope,id,owner={}) {
  const query=new URLSearchParams({scope,id});
  const record=await request('/api/v1/usage/compaction-record?'+query,owner);
  const url=URL.createObjectURL(new Blob([JSON.stringify(record,null,2)],{type:'application/json'}));
  const link=n('a');link.href=url;link.download=`compact-${id}.json`;document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);
}

export function createCompaction({locale,request,scope,owner={}}) {
  const element=n('details','compact-record'),summary=n('summary'),body=n('div','compact-details'),error=n('p','inline-error');
  const t=key=>translate(locale(),'compact.'+key);
  let current;
  const download=action('',async()=>{download.disabled=true;error.textContent='';try{await downloadCompaction(request,scope,current.id,owner);}catch{error.textContent=t('downloadFailed');}finally{download.disabled=false;}});
  element.append(summary,body,download,error);
  return {element,update(message){
    current=message.compaction;
    summary.textContent=t('title')+' · '+t(current.state);
    const duration=current.finished_at_ms?Math.max(0,current.finished_at_ms-message.created_at_ms):null;
    const size=current.state==='complete'?`${current.before.toLocaleString(locale())} → ${current.after.toLocaleString(locale())}`:current.before.toLocaleString(locale());
    body.textContent=t(current.reason)+' · '+size+' tokens'+(duration==null?'':` · ${(duration/1000).toLocaleString(locale())}s`);
    download.textContent=t('download');
  }};
}
