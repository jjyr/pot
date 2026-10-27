import {communication} from './communication.mjs';
import {addressKey} from './task-identity.mjs';
import {activeTool} from './tool-calls.mjs';

const callKey=tool=>JSON.stringify([tool?.turn_id||'',tool?.id]);
export const failedWork=tool=>!!tool&&(!!tool.failurePhase||['failed','denied'].includes(tool.phase));
// An aggregate exec can be waiting on a child. Only the actual approval is a
// boundary; its wrapper must not create another approval or hide that child.
export function processRecord(message,blocked=()=>false){
  if(message.error||message.tool&&blocked(message))return false;
  if(message.compaction)return !['failed','error'].includes(message.compaction.state);
  const tool=message.role==='tool'&&message.tool;
  const phase=tool?.script?.calls[0]?.phase||tool?.phase;
  return !!tool&&!['unknown','stopped'].includes(tool.phase)&&phase!=='waiting_approval';
}
export function mailIdentity(mail){
  const from=addressKey(mail.from),to=addressKey(mail.to);
  return typeof mail.id==='string'&&mail.id&&from&&to?JSON.stringify([from,to,mail.id]):null;
}

// Logical siblings, not mounted rows. No source mutations, sorting by Task,
// movement across prose or grouping across turns. Folded polls have already
// updated their original execution in projectScriptRuns.
export function createWorkRecordProjection(){
  let previous=new Map();
  return (messages,{blocked,anchor,actor}={})=>{
    const result=[],next=new Map(),used=new Set(),seenMail=new Set();
    const children=new Set(messages.filter(m=>m.tool?.parent_id).map(m=>JSON.stringify([m.tool.turn_id||'',m.tool.parent_id])));
    const anchoredGroup=previous.get(messages.find(message=>message.id===anchor)?.viewKey);
    let run=[],turn=null,contextTurn=null;
    const flush=()=>{
      if(!run.length)return;
      const retained=run.find(message=>message.id===anchor&&previous.has(message.viewKey)&&!used.has(previous.get(message.viewKey)))
        ||run.find(message=>previous.has(message.viewKey)&&!used.has(previous.get(message.viewKey))&&(previous.get(message.viewKey)!==anchoredGroup||run.some(member=>member.id===anchor)));
      let key=previous.get(retained?.viewKey)||`work:${run[0].viewKey}`;
      if(!retained&&key===anchoredGroup&&!run.some(member=>member.id===anchor))key+=':split';
      while(used.has(key))key+=':split';
      used.add(key);
      const members=run.map(message=>{
        const model=communication(message.tool,actor);
        if(!model||model.kind==='delegate')return message;
        const fresh=model.messages.filter(mail=>{const key=mailIdentity(mail);if(!key)return true;if(seenMail.has(key))return false;seenMail.add(key);return true;});
        // Keep duplicate projections inspectable as receipts at their original
        // location, with raw evidence; do not repeat their message body/count.
        return fresh.length===model.messages.length?message:{...message,workMail:fresh,workReceipt:!fresh.length};
      });
      result.push({id:key,viewKey:key,role:'tool',members,work:workRecordSummary(members,{actor,children})});
      for(const message of run)next.set(message.viewKey,key);
      run=[];
    };
    for(const message of messages){
      if(message.role==='user'||message.role==='system'&&!message.compaction)contextTurn=message.request_id||message.id;
      const identity=message.tool?.turn_id||message.turn_id||contextTurn;
      // The running tool group already supplies this one activity indicator.
      if(message.role==='assistant'&&!message.text&&!message.error&&!message.presentation&&message.phase==='tools'&&run.some(m=>activeTool(m.tool)))continue;
      if(processRecord(message,blocked)){
        if(run.length&&identity!==turn)flush();
        turn=identity;contextTurn=identity;run.push(message);
      }else{flush();result.push(message);}
    }
    flush();previous=next;return result;
  };
}

export function workRecordSummary(members,{actor,children=new Set()}={}){
  const participants=new Map(),failures=new Set(),seen=new Set();let tools=0,messages=0,active=null;
  for(const member of members){
    const tool=member.tool,model=communication(tool,actor),wrapper=children.has(callKey(tool))&&['exec','parallel','multi_tool_use.parallel'].includes(tool?.name);
    if(model&&model.kind!=='delegate'){
      for(const mail of model.messages)for(const address of [mail.from,mail.to]){
        const key=addressKey(address);
        if(key&&key!==addressKey(actor)&&!participants.has(key))participants.set(key,address);
      }
      for(const mail of member.workMail||model.messages){const key=mailIdentity(mail);if(!key||!seen.has(key)){messages++;if(key)seen.add(key);}}
    }else if(tool&&!wrapper)tools++;
    if(failedWork(tool))failures.add(JSON.stringify([tool.turn_id||'',tool.failureID||tool.id]));
    if(activeTool(tool)&&!wrapper)active=tool;
    if(member.compaction?.state==='running'&&!active)active={name:'compaction'};
  }
  return {tools,messages,failures:failures.size,participants:[...participants.values()],active};
}
