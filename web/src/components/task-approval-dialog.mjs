import {node as n,action} from './primitives.mjs';

export function createTaskApprovalDialog({locale}) {
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en;
  const dialog=n('dialog','task-approval-dialog'),header=n('div','task-approval-heading'),title=n('h2'),summary=n('p','hint'),content=n('div','task-approval-content');
  title.id=`task-approval-${crypto.randomUUID()}`;dialog.setAttribute('aria-labelledby',title.id);
  const later=action('',()=>dialog.close());header.append(title,later);dialog.append(header,summary,content);document.body.append(dialog);
  let context='',entries=[],seen=new Set();
  function show() {
    if(!entries.length)return;
    if(!dialog.open&&document.querySelector('dialog[open]'))return;
    if(!dialog.open)dialog.showModal();
    entries.forEach(entry=>seen.add(entry.id));
  }
  function update(nextContext,nextEntries,visible) {
    if(context!==nextContext){context=nextContext;seen=new Set();dialog.close();}
    entries=nextEntries;
    title.textContent=tr('Tasks need your approval','任务需要你授权');later.textContent=tr('Review later','稍后处理');
    const count=new Set(entries.map(entry=>entry.task)).size;
    summary.textContent=tr(`${count} task(s) are waiting for your approval. Review each operation below.`,`以下 ${count} 项任务正在等待你的授权，请逐项核对并选择允许或拒绝。`);
    const nodes=entries.map(entry=>entry.element);
    for(const node of [...content.childNodes])if(!nodes.includes(node))node.remove();
    nodes.forEach((node,index)=>{if(content.childNodes[index]!==node)content.insertBefore(node,content.childNodes[index]||null);});
    if(!visible||!entries.length){dialog.close();return;}
    if(entries.some(entry=>!seen.has(entry.id)))show();
  }
  return {update,show,dispose(){dialog.close();dialog.remove();}};
}
