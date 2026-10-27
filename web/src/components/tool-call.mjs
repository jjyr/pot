import {activeTool,toolResult,toolSummary,toolFailure} from '../state/tool-calls.mjs';
import {translate} from '../locales/messages.mjs';
import {createToolContent} from './tool-content.mjs';
import {toolKind,presentationLabel,presentationPreview} from '../state/tool-presentation.mjs';
import {createApprovalCard} from './approval-card.mjs';
import {messageAddresses} from '../state/message-identities.mjs';
import {assistantMention} from './assistant-mention.mjs';
import {communication} from '../state/communication.mjs';
import {createCommunication} from './communication.mjs';
import {agentSource} from './connection-visuals.mjs';
import {taskIdentities,addressKey} from '../state/task-identity.mjs';
import {communicationParticipant} from './communication-participant.mjs';
const n=(tag,cls='',text)=>{const node=document.createElement(tag);node.className=cls;if(text!==undefined)node.textContent=text;return node;};
const pretty=value=>typeof value==='string'?value:JSON.stringify(value??null,null,2);
const LIMIT=24000;
const clocks=new Set();let clockFrame=0,lastTick=0;
function scheduleClock(update){clocks.add(update);if(clockFrame)return;const frame=now=>{clockFrame=0;if(now-lastTick>=1000){lastTick=now;for(const tick of clocks)if(!tick())clocks.delete(tick);}if(clocks.size)clockFrame=requestAnimationFrame(frame);};clockFrame=requestAnimationFrame(frame);}


// Stable nodes preserve native disclosure, selection and output scroll on updates.
export function createToolCall({memory=new Map(),key='',locale=()=> 'en',resolveMessageIdentity=()=>undefined,messageActor=()=>undefined,onOpenTask,resolveTask,machineLabel=()=>translate(locale(),'machine.unavailable'),onDecision,onLocate,onInteract=()=>{},downloadFile,acquireFileImage}={}){
  const t=name=>translate(locale(),`tool.${name}`),root=n('details','tool-call'),summary=n('summary','tool-summary');
  root.dataset.cacheKey=`tool:${key}`;
  summary.addEventListener('click',onInteract);
  const title=n('span','tool-title'),excerpt=n('span','tool-excerpt'),status=n('span','tool-status'),duration=n('span','tool-duration');summary.append(title,excerpt,status,duration);
  const context=n('div','tool-context'),failure=n('p','tool-failure'),body=n('div','tool-body'),metadata=n('dl','tool-metadata'),approval=n('div','tool-approval'),children=n('div','tool-children'),sections=n('div'),raw=n('details','tool-raw'),rawTitle=n('summary'),rawBody=n('div');raw.append(rawTitle,rawBody);body.append(failure,metadata,approval,children,sections,raw);summary.append(context);root.append(summary,body);
  const communicationView=createCommunication({locale,resolveIdentity:resolveMessageIdentity,downloadFile,acquireFileImage,onOpenTask,resolveTask});
  summary.append(communicationView.summary);body.insertBefore(communicationView.body,sections);
  let current,options={},signature='',approvalID='',approvalCard=null,rawSignature='',autoOpened=false,building=false,refreshStatus=()=>{};
  let bodyVersion=[];
  const contentView=createToolContent({locale,downloadFile,acquireFileImage,onOpenTask,resolveTask});
  sections.append(contentView.element);
  // Ordinary communication starts collapsed; only explicit reading/navigation or
  // the existing approval path opens it. Updates never reapply a default.
  root.open=memory.get(key)?.open||false;raw.open=memory.get(key)?.raw||false;
  function messageTitle(tool){
    const model=communication(tool,messageActor());
    if(model?.kind==='delegate'){const target=communicationView.taskTarget(model);title.textContent=target.named?t('delegateTaskTo').replace('{name}','@'+target.label):t('delegateTask');title.title=title.textContent;return true;}
    const addresses=messageAddresses(tool);if(!addresses)return false;
    if(model?.kind==='receive'&&model.sources.length>1){title.textContent=t(model.sources.every(address=>address?.kind==='assistant')?'receiveMessageSources':'receiveMessageParticipants').replace('{count}',model.sources.length.toLocaleString(locale()));title.title=title.textContent;return true;}
    const participants=n('span'),seen=new Set();
    for(const address of addresses.length?addresses:[undefined]){
      const identity=JSON.stringify([address?.node_id,address?.kind,address?.agent_id]);if(seen.has(identity))continue;seen.add(identity);
      if(participants.childNodes.length)participants.append(document.createTextNode(', '));
      const profile=resolveMessageIdentity(address);
      if(address?.kind==='assistant'&&profile?.name){
        participants.append(assistantMention({label:profile.name,node:address.node_id,agent:address.agent_id}));
        const source=agentSource(profile.origin,locale());if(source)participants.append(source);
      }else participants.append(document.createTextNode(address?.kind==='task'?[profile?.name||t('messageTask'),taskIdentities.short(address)||t('messageTaskIDMissing')].join(' '):t(address?.kind==='user'?'messageUser':address?.kind==='assistant'?'messageAssistantUnavailable':'messageRecipientUnavailable')));
    }
    const [prefix,suffix]=t(tool.name==='receive_message'?'receiveMessageFrom':'sendMessageTo').split('{name}');
    title.replaceChildren(document.createTextNode(prefix),...participants.childNodes,document.createTextNode(suffix));
    if(model?.kind==='receive'&&model.count>1)title.append(document.createTextNode(t('messageCount').replace('{count}',model.count.toLocaleString(locale()))));
    title.title=title.textContent;return true;
  }
  const remember=()=>{if(!building)memory.set(key,{...memory.get(key),open:root.open,raw:raw.open});};root.addEventListener('toggle',()=>{remember();refreshStatus();if(current)update(current,options);});raw.addEventListener('toggle',()=>{remember();if(current)update(current,options);});
  // Populate on activation, before a second click can target an empty disclosure
  // whose position will change when its deferred toggle event renders content.
  summary.addEventListener('click',event=>{if(event.defaultPrevented||event.target.closest('button,a,input,select,textarea'))return;event.preventDefault();root.open=!root.open;remember();refreshStatus();if(current)update(current,options);});
  // Commit before ResizeObserver/virtual-window recycling can detach this row.
  rawTitle.addEventListener('click',event=>{event.preventDefault();onInteract(event);raw.open=!raw.open;remember();if(current)update(current,options);});
  function copyButton(label,get){const button=n('button','tool-action',label);button.type='button';button.onclick=async()=>{try{await navigator.clipboard.writeText(get());button.textContent=t('copied');}catch{button.textContent=t('copyFailed');}};return button;}
  function update(message,next={}){
    current=message;options=next;const tool=message.tool;if(!tool)return;
    root.classList.toggle('work-record-call',!!message.workRecord);
    summary.tabIndex=0;
    const result=tool.process?tool.process.result:toolResult(tool),pending=next.approval,script=tool.script;
    root.classList.toggle('script-run',!!script);
    const originalModel=communication(tool,messageActor()),model=message.workReceipt?null:originalModel&&message.workMail?{...originalModel,messages:message.workMail,count:message.workMail.length}:originalModel,summaryModel=model;
    if(model?.task)taskIdentities.register([model.task]);
    metadata.hidden=false;raw.hidden=false;
    if(metadata.parentElement!==rawBody)rawBody.prepend(metadata);
    communicationView.update(summaryModel,{expanded:root.open&&!pending,summaryOnly:!model,preparing:tool.phase==='preparing',compact:!!message.workRecord});
    communicationView.body.hidden=!model||!!pending;
    if(message.workRecord)communicationView.summary.hidden=true;
    root.classList.toggle('communication-tool',!!summaryModel);
    // Receive records are a history projection, not a timed network operation.
    duration.hidden=originalModel?.kind==='receive';

    const phase=pending?'waiting_approval':tool.phase;
    const messaging=message.workRecord?!!model:messageTitle(tool);root.classList.toggle('message-tool',messaging);
    if(!messaging)title.textContent=t(presentationLabel(tool));
    const preview=messaging||message.workRecord?'':presentationPreview(tool,{resolveTask})||(['terminal','patch','script','browser'].includes(toolKind(tool))?toolSummary(tool):'');
    excerpt.textContent=preview===tool.name||preview===title.textContent?'':preview;excerpt.title=excerpt.textContent;excerpt.hidden=!excerpt.textContent;
    const childTools=(next.children||[]).map(child=>child.tool);
    // Keep failures explicit in collapsed and expanded records.
    refreshStatus=()=>{
      let shown=phase;
      if(phase==='completed'&&result?.completed===false&&result?.exec_id&&!script)shown='scriptYielded';
      if(model?.kind==='receive'&&phase==='completed')shown='messageReceived';
      else if(model?.kind==='send'&&model.accepted)shown='messageAccepted';
      else if(model?.kind==='send'&&phase==='completed')shown='ended';
      else if(model?.kind==='delegate'&&phase==='completed')shown=model.task?'messageTaskCreated':'ended';
      status.textContent=t(next.offline&&activeTool(tool)?'reconnecting':shown);
      if(childTools.length&&!message.workRecord&&!script&&!tool.process){const complete=childTools.filter(child=>!activeTool(child)).length;if(complete<childTools.length&&!next.offline)status.textContent=t(childTools.filter(activeTool).every(child=>child.phase==='waiting_approval')?'waiting_approval':'scriptRunning');status.textContent+=` · ${complete}/${childTools.length}`;}
    };
    refreshStatus();
    if(message.workReceipt){title.textContent=translate(locale(),'work.receipt');}
    else if(message.workRecord&&model){
      const first=model.messages[0],actor=addressKey(messageActor()),from=addressKey(first?.from),to=addressKey(first?.to);
      const label=n('span','work-message-action',translate(locale(),'work.'+(model.kind==='delegate'?'create':from&&actor===from?'send':to&&actor===to?'receive':'message')));
      const identities=[];
      if(model.kind==='delegate'){
        const address=model.task?.address||first?.to;identities.push(communicationParticipant(address,{resolveIdentity:resolveMessageIdentity,locale}));
      }else if(actor&&(from===actor||to===actor)){
        identities.push(communicationParticipant(from===actor?first?.to:first?.from,{resolveIdentity:resolveMessageIdentity,locale}));
      }else if(first){
        identities.push(communicationParticipant(first.from,{resolveIdentity:resolveMessageIdentity,locale}),n('span','work-message-direction','→'),communicationParticipant(first.to,{resolveIdentity:resolveMessageIdentity,locale}));
      }
      title.replaceChildren(label,...identities);
      if(model.count>1)title.append(n('span','work-message-count',t('messageCount').replace('{count}',model.count.toLocaleString(locale()))));
    }else if(script){title.textContent=t('script');excerpt.hidden=true;excerpt.textContent='';}
    title.title=title.textContent;
    root.dataset.phase=phase;root.dataset.callId=tool.id;root.dataset.turnId=tool.turn_id||'';
    const start=script?tool.started_at_ms:phase==='waiting_approval'?(tool.waiting_at_ms||tool.started_at_ms):(tool.running_at_ms||tool.started_at_ms);
    // Live clocks run only while a visible operation needs one, and honor backend time.
    const end=tool.finished_at_ms||(activeTool(tool)?Date.now():null),elapsed=start&&end?Math.max(0,(end-start)/1000):null;
    duration.textContent=elapsed===null?'':`${phase==='waiting_approval'&&!script?t('waited')+' ':''}${elapsed<1?elapsed.toFixed(1):Math.floor(elapsed)}s`;
    const targetLabel=tool.target?.runtime_id?machineLabel(tool.target.runtime_id):'';
    context.textContent=[targetLabel,tool.workdir,tool.execution_mode==='host'?t('hostExecution'):tool.execution_mode==='sandbox'?t('sandboxExecution'):''].filter(Boolean).join(' · ');context.hidden=!activeTool(tool)||!context.textContent;
    const error=tool.diagnostic||(tool.failurePhase?t(tool.failurePhase):['failed','denied','unknown'].includes(phase)?String(toolFailure(tool)||t(phase)).split('\n')[0]:'');failure.textContent=String(error).split('\n')[0];failure.hidden=!error;
    failure.onclick=script?event=>{event.preventDefault();root.open=true;onLocate?.(tool.failureID||script.failureID,tool.turn_id);}:null;
    if(script&&error){failure.tabIndex=0;failure.setAttribute('role','button');failure.onkeydown=event=>{if(['Enter',' '].includes(event.key)){event.preventDefault();failure.click();}};}else{failure.removeAttribute('tabindex');failure.removeAttribute('role');failure.onkeydown=null;}
    const channels=model?[...new Set(model.messages.map(mail=>t(!mail.from?.node_id||!mail.to?.node_id?'channelUnknown':mail.from.node_id===mail.to.node_id?'channelLocal':'channelLink')))].join(', '):'';
    const nextSignature=JSON.stringify([tool.name,tool.target,tool.workdir,tool.execution_mode,targetLabel,channels,locale()]);
    if(signature!==nextSignature){signature=nextSignature;metadata.replaceChildren();for(const [label,value]of [[t('messageChannel'),channels],[t('executionMode'),tool.execution_mode==='host'?t('hostExecution'):tool.execution_mode==='sandbox'?t('sandboxExecution'):null],[t('name'),tool.name],[t('target'),targetLabel],[t('workdir'),tool.workdir]])if(value)metadata.append(n('dt','',label),n('dd','',value));}
    if(pending){
      if(approvalID!==pending.id){approvalID=pending.id;approvalCard=createApprovalCard({locale,machineLabel,onDecision});approval.replaceChildren(approvalCard.element);}
      approvalCard.update(pending);
    }else{approvalID='';approvalCard=null;approval.replaceChildren();}
    approval.hidden=!pending;
    if((pending||next.approvalDescendant)&&!autoOpened){building=true;root.open=true;autoOpened=true;building=false;remember();queueMicrotask(()=>{for(let parent=root.parentElement?.closest('details.tool-call');parent;parent=parent.parentElement?.closest('details.tool-call'))parent.open=true;});}
    if(!pending&&!next.approvalDescendant)autoOpened=false;
    if(activeTool(tool))scheduleClock(tick);
    // Collapsed cards need only their summary and approval identity. Defer
    // output Markdown, media and raw JSON until the reader asks to see them.
    if(!root.open)return;
    contentView.element.hidden=!!model;
    const version=[tool,tool.phase,tool.args,tool.result,tool.output?.length,tool.output?.at(-1)?.text,script?.output,tool.process?.result,locale(),pending,raw.open,next.children?.length,targetLabel];
    if(version.every((value,index)=>value===bodyVersion[index]))return;
    bodyVersion=version;
    if(!model)contentView.update(tool,{result,pending,targetLabel,hasChildren:!!next.children?.length,compact:!!message.workRecord});
    rawTitle.textContent=t(originalModel?'callDetails':'raw');if(!raw.open)return;
    const calls=tool.process?.calls||script?.calls||[tool],rawValue=JSON.stringify([locale(),calls]);
    if(rawSignature!==rawValue){rawSignature=rawValue;rawBody.replaceChildren(metadata);for(const call of calls){rawBody.append(n('p','tool-call-id',`${call.name} · ${call.id}`),n('p','tool-call-time',call.started_at_ms?new Date(call.started_at_ms).toLocaleString(locale()):''));for(const [label,value]of [[t('arguments'),call.args],[t('result'),call.result],[t('output'),call.output]])if(value!==undefined){const text=pretty(value),head=n('div','tool-section-heading');head.append(n('strong','',label),copyButton(t('copyJSON'),()=>text));rawBody.append(head,n('pre','tool-code',text.slice(0,LIMIT)));if(text.length>LIMIT)rawBody.append(n('p','tool-truncated',t('displayTruncated')));}}}
  }
  function tick(){if(!root.isConnected||!activeTool(current?.tool))return false;if(document.hidden)return true;const tool=current.tool,waiting=options.approval||tool.phase==='waiting_approval',start=tool.script?tool.started_at_ms:waiting?(tool.waiting_at_ms||tool.started_at_ms):tool.running_at_ms||tool.started_at_ms;if(start)duration.textContent=`${waiting&&!tool.script?t('waited')+' ':''}${Math.max(0,Math.floor((Date.now()-start)/1000))}s`;return true;}
  return {element:root,children,update};
}
