// Shared projection for durable chat messages and ordered Task events.
export const activeTool=tool=>['preparing','waiting_approval','running'].includes(tool?.phase);
export function applyToolUpdate(tool,update){
  const at=update.at_ms||0;
  switch(update.type){
    case'tool_started':Object.assign(tool,{id:update.id,parent_id:update.parent_id||'',name:update.name,args:update.args??{},phase:'preparing',started_at_ms:at,output:[]});break;
    case'tool_state':tool.phase=update.phase;if(update.execution_mode)tool.execution_mode=update.execution_mode;if(update.target)tool.target=update.target;if(update.workdir)tool.workdir=update.workdir;if(update.phase==='waiting_approval'&&!tool.waiting_at_ms)tool.waiting_at_ms=at;if(update.phase==='running'&&!tool.running_at_ms)tool.running_at_ms=at;break;
    case'tool_output':if(tool.phase==='preparing'){tool.phase='running';tool.running_at_ms=at;}tool.output??=[];if(tool.output.at(-1)?.stream===(update.stream||''))tool.output.at(-1).text+=update.text||'';else tool.output.push({stream:update.stream||'',text:update.text||''});break;
    case'tool_finished':tool.result=update.result;tool.finished_at_ms=at;if(tool.phase!=='denied')tool.phase=update.failed?'failed':'completed';break;
    case'turn_finished':tool.phase=update.outcome?.kind==='cancelled'?'stopped':'unknown';tool.finished_at_ms=at;tool.diagnostic=update.outcome?.error||'';break;
  }
  return tool;
}
const parsedResults=new WeakMap();
export function toolResult(tool){
  let result=tool?.result;
  if(typeof result==='string'){
    const cached=parsedResults.get(tool);if(cached?.source===result)return cached.value;
    let value;try{value=JSON.parse(result);}catch{value=result;}
    parsedResults.set(tool,{source:result,value});return value;
  }
  return result;
}
export function toolSummary(tool){
  const args=tool?.args||{};
  if(tool?.name==='receive_message')return (args.messages||[]).map(message=>message.from?.agent_id).filter(Boolean).filter((value,index,all)=>all.indexOf(value)===index).join(', ');
  if(tool?.name==='write_stdin')return '';
  if(['exec','cua_repl'].includes(tool?.name))return `${args.code||''}`.trim().split('\n')[0]||'exec';
  if(tool?.name==='apply_patch')return String(args.patch||args.input||'').split('\n').find(line=>/^\*\*\* (Add|Update|Delete) File:/.test(line))?.replace(/^\*\*\* /,'')||'apply_patch';
  return String(args.cmd||args.url||args.path||args.session_id||args.query||tool?.name||'');
}
export function toolFailure(tool){
  const result=toolResult(tool);return tool?.diagnostic||result?.error||result?.stderr||(result?.exit_code!=null&&result.exit_code!==0?`exit_code: ${result.exit_code}`:'');
}
export function toolForest(messages){
  const byID=new Map(messages.filter(message=>message.tool).map(message=>[`${message.tool.turn_id}:${message.tool.id}`,{message,children:[]}])) ,roots=[];
  for(const entry of byID.values()){
    const parent=byID.get(`${entry.message.tool.turn_id}:${entry.message.tool.parent_id}`);
    // Fence malformed parent cycles without hiding their calls.
    if(parent&&parent!==entry&&rootsForParent(parent,entry,byID))parent.children.push(entry);else roots.push(entry);
  }
  return {roots,byID};
}
function rootsForParent(parent,child,byID){const seen=new Set([child.message.tool.id]);for(let entry=parent;entry;entry=byID.get(`${entry.message.tool.turn_id}:${entry.message.tool.parent_id}`)){if(seen.has(entry.message.tool.id))return false;seen.add(entry.message.tool.id);}return true;}

export function patchFiles(patch=''){
  return [...String(patch).matchAll(/^\*\*\* (Add File|Update File|Delete File|Move to): (.+)$/gm)].map(([,operation,path])=>({operation:({'Add File':'added','Update File':'modified','Delete File':'deleted','Move to':'moved'})[operation],path}));
}
