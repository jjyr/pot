import {moveConversationNode} from './conversation-dom.mjs';
import {activeTool,toolResult,toolSummary,toolFailure} from '../state/tool-calls.mjs';
import {currentPreparedRender,preparedPreviousRoot,preparedDOM,preparedAsset,preparedText} from './prepared-render.mjs';
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
  const preparation=currentPreparedRender(),previousRoot=preparation?.context.phase==='prepare'?preparedPreviousRoot(preparation.context,preparation.member):null;
  const previousTool=previousRoot&&[previousRoot,...previousRoot.querySelectorAll('details.tool-call')].find(node=>node.dataset.cacheKey===root.dataset.cacheKey);
  const live=()=>root.isConnected&&!root.closest('[data-preparation-surface]')&&preparation?.context.phase!=='prepare'&&preparation?.context.alive!==false;
  summary.addEventListener('click',onInteract);
  const title=n('span','tool-title'),excerpt=n('span','tool-excerpt'),status=n('span','tool-status'),duration=n('span','tool-duration');summary.append(title,excerpt,status,duration);
  const context=n('div','tool-context'),failure=n('p','tool-failure'),body=n('div','tool-body'),metadata=n('dl','tool-metadata'),approval=n('div','tool-approval'),children=n('div','tool-children'),sections=n('div'),raw=n('details','tool-raw'),rawTitle=n('summary'),rawBody=n('div');raw.append(rawTitle,rawBody);body.append(failure,metadata,approval,children,sections,raw);summary.append(context);root.append(summary,body);
  const communicationView=createCommunication({locale,resolveIdentity:resolveMessageIdentity,downloadFile,acquireFileImage,onOpenTask,resolveTask});
  summary.append(communicationView.summary);body.insertBefore(communicationView.body,sections);
  let current,options={},signature='',approvalID='',approvalSignature='',approvalCard=null,rawSignature='',autoOpened=previousTool?.toolAutoOpened||false,building=false,refreshStatus=()=>{};
  let bodyVersion=[];
  const contentView=createToolContent({locale,downloadFile,acquireFileImage,onOpenTask,resolveTask});
  sections.append(contentView.element);
  // Ordinary communication starts collapsed; only explicit reading/navigation or
  // the existing approval path opens it. Updates never reapply a default.
  root.open=memory.get(key)?.open??previousTool?.open??false;raw.open=memory.get(key)?.raw??previousTool?.querySelector(':scope > .tool-body > .tool-raw')?.open??false;
  function messageTitle(tool){
    const model=communication(tool,messageActor());
    if(model?.kind==='delegate'){const target=communicationView.taskTarget(model);preparedText(title,'tool-call:title',target.named?t('delegateTaskTo').replace('{name}','@'+target.label):t('delegateTask'));title.title=title.textContent;return true;}
    const addresses=messageAddresses(tool);if(!addresses)return false;
    if(model?.kind==='receive'&&model.sources.length>1){preparedText(title,'tool-call:title',t(model.sources.every(address=>address?.kind==='assistant')?'receiveMessageSources':'receiveMessageParticipants').replace('{count}',model.sources.length.toLocaleString(locale())));title.title=title.textContent;return true;}
    const identityRevision=JSON.stringify([tool.name,model,addresses,locale(),addresses.map(address=>resolveMessageIdentity(address))]);
    preparedDOM(title,'tool-message-title',identityRevision,target=>{
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
    target.replaceChildren(document.createTextNode(prefix),...participants.childNodes,document.createTextNode(suffix));
    if(model?.kind==='receive'&&model.count>1)target.append(document.createTextNode(t('messageCount').replace('{count}',model.count.toLocaleString(locale()))));
    });
    title.title=title.textContent;return true;
  }
  const remember=()=>{if(!building&&live())memory.set(key,{...memory.get(key),open:root.open,raw:raw.open});};root.addEventListener('toggle',()=>{if(!live())return;remember();if(root.closest('.prepared-row'))return;refreshStatus();if(current)update(current,options);});raw.addEventListener('toggle',()=>{if(!live())return;remember();if(current)update(current,options);});
  // Populate on activation, before a second click can target an empty disclosure
  // whose position will change when its deferred toggle event renders content.
  function disclose(field,node){
    if(!live())return;
    if(root.closest('.prepared-row')){
      memory.set(key,{...memory.get(key),[field]:!(memory.get(key)?.[field]??node.open)});
      root.dispatchEvent(new CustomEvent('pot:prepare-resize',{bubbles:true}));return;
    }
    node.open=!node.open;remember();if(root.closest('.prepared-row'))return;refreshStatus();if(current)update(current,options);
  }
  summary.addEventListener('click',event=>{if(event.defaultPrevented||event.target.closest('button,a,input,select,textarea'))return;event.preventDefault();disclose('open',root);});
  // Commit before ResizeObserver/virtual-window recycling can detach this row.
  rawTitle.addEventListener('click',event=>{event.preventDefault();onInteract(event);disclose('raw',raw);});
  function copyButton(label,get){const button=n('button','tool-action',label);button.type='button';button.onclick=async()=>{try{await navigator.clipboard.writeText(get());button.textContent=t('copied');}catch{button.textContent=t('copyFailed');}};return button;}
  function update(message,next={}){
    current=message;options=next;const tool=message.tool;if(!tool)return;
    if(currentPreparedRender()){
      const desired=memory.get(key);if(desired){if(desired.open!==undefined)root.open=desired.open;if(desired.raw!==undefined)raw.open=desired.raw;}
    }
    if(next.expanded)root.open=true;
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
    const messaging=message.workReceipt?false:message.workRecord?!!model:messageTitle(tool);root.classList.toggle('message-tool',messaging);
    if(!messaging&&!message.workReceipt&&!script&&!(message.workRecord&&model))preparedText(title,'tool-call:title',t(presentationLabel(tool)));
    const preview=messaging||message.workRecord?'':presentationPreview(tool,{resolveTask})||(['terminal','patch','script','browser'].includes(toolKind(tool))?toolSummary(tool):'');
    if(!script)preparedText(excerpt,'tool-call:excerpt',preview===tool.name||preview===title.textContent?'':preview);excerpt.title=excerpt.textContent;excerpt.hidden=!excerpt.textContent;
    const childTools=(next.children||[]).map(child=>child.tool);
    // Keep failures explicit in collapsed and expanded records.
    refreshStatus=()=>{
      let shown=phase;
      if(phase==='completed'&&result?.completed===false&&result?.exec_id&&!script)shown='scriptYielded';
      if(model?.kind==='receive'&&phase==='completed')shown='messageReceived';
      else if(model?.kind==='send'&&model.accepted)shown='messageAccepted';
      else if(model?.kind==='send'&&phase==='completed')shown='ended';
      else if(model?.kind==='delegate'&&phase==='completed')shown=model.task?'messageTaskCreated':'ended';
      let text=t(next.offline&&activeTool(tool)?'reconnecting':shown);
      if(childTools.length&&!message.workRecord&&!script&&!tool.process){const complete=childTools.filter(child=>!activeTool(child)).length;if(complete<childTools.length&&!next.offline)text=t(childTools.filter(activeTool).every(child=>child.phase==='waiting_approval')?'waiting_approval':'scriptRunning');text+=` · ${complete}/${childTools.length}`;}
      preparedText(status,'tool-status',text);
    };
    refreshStatus();
    if(message.workReceipt){preparedText(title,'tool-call:title',translate(locale(),'work.receipt'));}
    else if(message.workRecord&&model){
      preparedDOM(title,'work-message-title',JSON.stringify([model,messageActor(),locale(),model.messages.flatMap(mail=>[resolveMessageIdentity(mail.from),resolveMessageIdentity(mail.to)])]),target=>{
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
      target.replaceChildren(label,...identities);
      if(model.count>1)target.append(n('span','work-message-count',t('messageCount').replace('{count}',model.count.toLocaleString(locale()))));
      });
    }else if(script){preparedText(title,'tool-call:title',t('script'));excerpt.hidden=true;preparedText(excerpt,'tool-call:excerpt','');}
    title.title=title.textContent;
    root.dataset.phase=phase;root.dataset.callId=tool.id;root.dataset.turnId=tool.turn_id||'';
    const start=script?tool.started_at_ms:phase==='waiting_approval'?(tool.waiting_at_ms||tool.started_at_ms):(tool.running_at_ms||tool.started_at_ms);
    // Live clocks run only while a visible operation needs one, and honor backend time.
    const end=tool.finished_at_ms||(activeTool(tool)?currentPreparedRender()?.context.now??Date.now():null),elapsed=start&&end?Math.max(0,(end-start)/1000):null;
    preparedText(duration,'tool-call:duration',elapsed===null?'':`${phase==='waiting_approval'&&!script?t('waited')+' ':''}${elapsed<1?elapsed.toFixed(1):Math.floor(elapsed)}s`);
    const targetLabel=tool.target?.runtime_id?machineLabel(tool.target.runtime_id):'';
    preparedText(context,'tool-call:context',[targetLabel,tool.workdir,tool.execution_mode==='host'?t('hostExecution'):tool.execution_mode==='sandbox'?t('sandboxExecution'):''].filter(Boolean).join(' · '));context.hidden=!activeTool(tool)||!context.textContent;
    const error=tool.diagnostic||(tool.failurePhase?t(tool.failurePhase):['failed','denied','unknown'].includes(phase)?String(toolFailure(tool)||t(phase)).split('\n')[0]:'');preparedText(failure,'tool-call:failure',String(error).split('\n')[0]);failure.hidden=!error;
    failure.onclick=script?event=>{event.preventDefault();if(root.closest('.prepared-row')){memory.set(key,{...memory.get(key),open:true});root.dispatchEvent(new CustomEvent('pot:prepare-resize',{bubbles:true}));}else root.open=true;onLocate?.(tool.failureID||script.failureID,tool.turn_id);}:null;
    if(script&&error){failure.tabIndex=0;failure.setAttribute('role','button');failure.onkeydown=event=>{if(['Enter',' '].includes(event.key)){event.preventDefault();failure.click();}};}else{failure.removeAttribute('tabindex');failure.removeAttribute('role');failure.onkeydown=null;}
    const channels=model?[...new Set(model.messages.map(mail=>t(!mail.from?.node_id||!mail.to?.node_id?'channelUnknown':mail.from.node_id===mail.to.node_id?'channelLocal':'channelLink')))].join(', '):'';
    const nextSignature=JSON.stringify([tool.name,tool.target,tool.workdir,tool.execution_mode,targetLabel,channels,locale()]);
    if(signature!==nextSignature){signature=nextSignature;preparedDOM(metadata,'tool-metadata',nextSignature,target=>{target.replaceChildren();for(const [label,value]of [[t('messageChannel'),channels],[t('executionMode'),tool.execution_mode==='host'?t('hostExecution'):tool.execution_mode==='sandbox'?t('sandboxExecution'):null],[t('name'),tool.name],[t('target'),targetLabel],[t('workdir'),tool.workdir]])if(value)target.append(n('dt','',label),n('dd','',value));});}
    if(pending){
      const value=JSON.stringify(pending),environment=currentPreparedRender();
      if(environment?.context.phase==='prepare'){
        if(approvalID!==pending.id){approvalID=pending.id;approvalCard=createApprovalCard({locale,machineLabel,onDecision});approval.replaceChildren(approvalCard.element);}
        approvalCard.update(pending);preparedAsset('inline-approval',value,()=>approvalCard);
      }else if(environment?.context.phase==='commit'){
        const candidate=preparedAsset('inline-approval',value,()=>{throw Error('Missing prepared inline approval');});
        if(!approvalCard||approvalSignature!==value||approvalCard.element.open!==candidate.element.open){approvalCard?.invalidate();approvalCard=candidate;moveConversationNode(approval,approvalCard.element);for(const node of [...approval.childNodes])if(node!==approvalCard.element)node.remove();}
        approvalID=pending.id;
      }else{if(approvalID!==pending.id){approvalID=pending.id;approvalCard=createApprovalCard({locale,machineLabel,onDecision});approval.replaceChildren(approvalCard.element);}approvalCard.update(pending);}
      approvalSignature=value;
    }else{approvalID='';approvalCard=null;approval.replaceChildren();}
    approval.hidden=!pending;
    if((pending||next.approvalDescendant)&&!autoOpened){building=true;root.open=true;autoOpened=true;building=false;remember();queueMicrotask(()=>{for(let parent=root.parentElement?.closest('details.tool-call');parent;parent=parent.parentElement?.closest('details.tool-call'))parent.open=true;});}
    if(!pending&&!next.approvalDescendant)autoOpened=false;
    root.toolAutoOpened=autoOpened;
    if(activeTool(tool))scheduleClock(tick);
    // Collapsed cards need only their summary and approval identity. Defer
    // output Markdown, media and raw JSON until the reader asks to see them.
    if(!root.open)return;
    contentView.element.hidden=!!model;
    const version=[tool,tool.phase,tool.args,tool.result,tool.output?.length,tool.output?.at(-1)?.text,script?.output,tool.process?.result,locale(),pending,raw.open,next.children?.length,targetLabel];
    if(version.every((value,index)=>value===bodyVersion[index])&&currentPreparedRender()?.context.phase!=='commit')return;
    bodyVersion=version;
    if(!model)contentView.update(tool,{result,pending,targetLabel,hasChildren:!!next.children?.length,compact:!!message.workRecord});
    preparedText(rawTitle,'tool-call:rawTitle',t(originalModel?'callDetails':'raw'));if(!raw.open)return;
    const calls=tool.process?.calls||script?.calls||[tool],rawValue=JSON.stringify([locale(),calls]);
    if(rawSignature!==rawValue){rawSignature=rawValue;
      preparedDOM(rawBody,'tool-raw',rawValue,target=>{target.replaceChildren();for(const call of calls){target.append(n('p','tool-call-id',`${call.name} · ${call.id}`),n('p','tool-call-time',call.started_at_ms?new Date(call.started_at_ms).toLocaleString(locale()):''));for(const [label,value]of [[t('arguments'),call.args],[t('result'),call.result],[t('output'),call.output]])if(value!==undefined){const text=pretty(value),head=n('div','tool-section-heading');head.append(n('strong','',label),copyButton(t('copyJSON'),()=>text));target.append(head,n('pre','tool-code',text.slice(0,LIMIT)));if(text.length>LIMIT)target.append(n('p','tool-truncated',t('displayTruncated')));}}},projection=>({retain:[[projection.querySelector(':scope > .tool-metadata'),metadata]]}));
      // Metadata is a stable separately prepared slot, not part of raw receipts.
      if(metadata.parentElement!==rawBody)rawBody.prepend(metadata);
    }
  }
  function tick(){
    if(!root.isConnected||!activeTool(current?.tool))return false;
    if(document.hidden||!live())return true;
    const tool=current.tool,waiting=options.approval||tool.phase==='waiting_approval',start=tool.script?tool.started_at_ms:waiting?(tool.waiting_at_ms||tool.started_at_ms):tool.running_at_ms||tool.started_at_ms;
    if(start){
      const text=`${waiting&&!tool.script?t('waited')+' ':''}${Math.max(0,Math.floor((Date.now()-start)/1000))}s`;
      if(duration.textContent!==text){
        if(root.closest('.prepared-row'))root.dispatchEvent(new CustomEvent('pot:prepare-resize',{bubbles:true}));
        else preparedText(duration,'tool-call:duration',text);
      }
    }
    return true;
  }
  return {element:root,children,update,prepareApproval(value){if(approvalCard&&(value?.fresh===false||JSON.stringify(value)!==JSON.stringify(options.approval)))approvalCard.invalidate();}};
}
