import {taskElapsed,taskClockEnd} from './task-time.mjs';
import {taskIdentityMark,taskIdentityShort} from './task-identity.mjs';
import {reconcile} from './reconcile.mjs';
import {taskIdentities,taskAddress} from '../state/task-identity.mjs';
import {linkDisplayName,machinePresentation} from '../state/machine-identity.mjs';
import {createCompaction} from './compaction.mjs';
import {taskAgentSource} from './connection-visuals.mjs';
import {agentFailureKey,supportsCapability} from '../state/agent-links.mjs';
import {node as n,action} from './primitives.mjs';
import {icon} from './icons.mjs';
import {translate} from '../locales/messages.mjs';
import {bindComposer} from './composer.mjs';
import {bindCompletions} from './completions.mjs';
import {createReplyBubble} from './reply.mjs';
import {createToolCall} from './tool-call.mjs';
import {messageIdentities} from '../state/message-identities.mjs';
import {renderMarkdown} from './markdown.mjs';
import {toolForest} from '../state/tool-calls.mjs';
import {projectScriptRuns} from '../state/script-runs.mjs';
import {projectTaskMessages} from '../state/task-transcript.mjs';
import {createCommunicationWindow} from './communication-window.mjs';
import {copyFeedback} from './motion.mjs';

export function createTaskConversation({root,task,record:initial,locale,client,scope,draft,onDraft,downloadFile:downloadTaskFile,toolMemory,approvalFor,onDecision,onSend,onStop,onPause,onCancel,onArchive,onUsage,onRefresh,onSettings,onOpenTask,assistantName,onEarlier,onLatest}){
  const machineLabel=id=>machinePresentation(client).label(id,scope,locale());
  const acquireFileImage=client.acquireFileImage?({task,path})=>client.acquireFileImage(task,path,scope):undefined;
  const resourceOptions={messageFiles:client.messageFiles?(message,options)=>client.messageFiles(message,{...scope,...options,task:task.id}):undefined,retryMessageFile:client.retryMessageFile?(message,ref,options)=>client.retryMessageFile(message,ref,{...scope,...options,task:task.id}):undefined,acquireResource:client.acquireResource?resource=>client.acquireResource(resource,scope):undefined,downloadResource:client.downloadResource?resource=>client.downloadResource(resource,scope):undefined};
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en,t=key=>translate(locale(),key);
  const label=(button,text)=>{button.title=text;button.setAttribute('aria-label',text);};
  const iconButton=(name,text,className='icon-button')=>{const button=action('',()=>{},{});button.className=className;button.append(icon(name));label(button,text);return button;};
  const stage=n('div','task-stage'),stageInner=n('div','task-stage-inner'),stageLabel=n('span','task-status'),locate=action(tr('View approval','查看授权'),locateApproval),retry=action(tr('Retry','重试'),()=>run(onRefresh));
  stage.setAttribute('role','status');stageInner.append(stageLabel,locate,retry);stage.append(stageInner);
  const history=n('div','task-history'),column=n('div','task-column'),goal=n('details','task-goal'),goalSummary=n('summary'),goalPreview=n('span','task-goal-preview'),goalBody=n('div','message-body task-goal-body');
  goal.dataset.scrollAnchor='task-goal';goal.dataset.cacheKey='task-goal';goalSummary.append(icon('chevron-right'),n('span','task-goal-label',tr('Task goal','任务目标')),goalPreview);goal.append(goalSummary,goalBody);
  const initialIdentity={...(initial.info||task),_space_id:scope.space,assistant_id:scope.owner},identityMark=taskIdentityMark(initialIdentity),shortID=taskIdentityShort(initialIdentity,t('tasks.identityUnknown'));shortID.classList.add('task-short-id');goalSummary.insertBefore(identityMark,goalPreview);goalSummary.insertBefore(shortID,goalPreview);
  function updateIdentity(){const info={...(record?.info||initial.info||task),_space_id:scope.space,assistant_id:scope.owner};taskIdentities.register([info]);shortID.textContent=taskIdentities.short(info)||t('tasks.identityUnknown');shortID.title=taskAddress(info)?JSON.stringify(taskAddress(info)):t('tasks.identityUnknown');reconcile(identityMark,taskIdentityMark(info));}
  goalPreview.textContent=(initial.request?.text||'').replace(/\s+/g,' ');goalBody.append(renderMarkdown(initial.request?.text||'',{downloadFile,acquireFileImage}));
  const historyNotice=n('p','hint'),boundary=n('div','task-history-boundary');boundary.style.minHeight='1px';boundary.setAttribute('role','status');
  const transcript=n('div','task-transcript'),files=n('div','task-files');files.dataset.scrollAnchor='task-files';transcript.setAttribute('role','log');transcript.setAttribute('aria-label',tr('Task conversation','任务对话'));
  const downloadNotice=n('div','task-download-error');downloadNotice.hidden=true;downloadNotice.setAttribute('role','alert');
  const approvalHost=n('div','task-unmatched-approvals');column.append(goal,historyNotice,boundary,approvalHost,transcript,files,downloadNotice);history.append(column);
  const composerArea=n('div','composer-area task-composer-area'),form=n('form','composer'),input=n('textarea','composer-input'),send=iconButton('stop',tr('Stop task','停止任务'),'send-button'),latest=iconButton('down',t('latest'),'latest-button'),footer=n('p','task-ended'),error=n('p','task-action-error');
  input.rows=1;input.id=`task-composer-${task.id}`;input.setAttribute('autocapitalize','sentences');input.setAttribute('aria-label',t('tasks.followupLabel'));input.placeholder=t('tasks.followupPlaceholder');
  input.value=draft;form.append(input,send);composerArea.append(latest,form,footer,error);latest.hidden=true;footer.hidden=true;error.hidden=true;error.setAttribute('role','alert');
  root.classList.add('task-conversation');root.append(stage,history,composerArea);
  const dialog=n('dialog','preferences product-actions task-actions');dialog.setAttribute('aria-label',tr('Task actions','任务操作'));root.append(dialog);
  let fresh=false,record=initial,state,inputs=[],approvals=[],unavailable=false,connectionError='',storageError=false,busy=false,historyBusy=false,disposed=false,frame=0,menuMode='actions',menuSignature='',fileSignature='',draftVersion=0;
  let forest,projection,downloadFailure,historyFailure,historyAbort,historyAttempt='',historyRun='',historyFrame=0;
  function renderDownloadFailure(){
    downloadNotice.hidden=!downloadFailure;downloadNotice.replaceChildren();
    if(!downloadFailure)return;
    const details=n('details');details.append(n('summary','',t('errorDetails')),n('pre','',downloadFailure.diagnostic));
    downloadNotice.append(n('p','hint',t('resource.downloadFailed')),action(t('retry'),()=>downloadFile(downloadFailure.file)),details);
  }
  async function downloadFile(file){
    if(disposed)return;
    downloadFailure=null;renderDownloadFailure();
    try{await downloadTaskFile(file);}
    catch(reason){if(!disposed){downloadFailure={file,diagnostic:reason?.message||String(reason)};renderDownloadFailure();}}
  }
  const heights=new Map();
  const transcriptWindow=createCommunicationWindow({onOpenTask,locale,actor:()=>record.info?.address||task.address,resolveIdentity:address=>messageIdentities(client).resolve(address),blocked:message=>!!approvalFor(message.tool),memory:toolMemory,scope:JSON.stringify([scope,task.id]),scroller:history,host:transcript,render:renderMessage,dispose:entry=>disposeMessage(entry),requestRender:scheduleFollow,heights,active:()=>!disposed&&root.isConnected&&!!history.clientHeight,upward:scheduleHistory,scrolled:scheduleHistory,earlierBoundary:boundary,onEarlierVisible:scheduleHistory,canReadEarlier:()=>!!state?.has_more});
  const stopReading=transcriptWindow.subscribeReading(()=>{latest.hidden=transcriptWindow.following;});
  const editor=bindComposer(input,{onInput:text=>{draftVersion++;onDraft(completion.value());updateComposer();},onSend:submit,onCompositionEnd:updateComposer});
  const completion=bindCompletions(input,{client:{scope:()=>scope,resources:client.resources,read:client.read?(path,options)=>client.read(path,options):undefined,request:(path,options)=>client.request(path,options)},host:composerArea,locale,mobile:editor.mobile,id:`pot-task-${task.id}-completions`,setText:text=>editor.setValue(text)});
  input.value='';completion.setValue(draft);
  const can=operation=>supportsCapability(record,operation);
  const linked=()=>record.info?.agent_link||task.agent_link;
  const active=()=>['queued','running','waiting','waiting_approval'].includes(record.info?.phase||task.phase);
  function scheduleFollow(){if(frame||disposed)return;frame=requestAnimationFrame(()=>{frame=0;if(!disposed)renderTranscript();});}
  let previousWidth=0,resizeFrame=0;
  const resizeObserver=new ResizeObserver(()=>{if(root.clientWidth!==previousWidth){previousWidth=root.clientWidth;cancelAnimationFrame(resizeFrame);resizeFrame=requestAnimationFrame(()=>{if(!disposed)editor.resize();});}});resizeObserver.observe(column);resizeObserver.observe(history);resizeObserver.observe(composerArea);
  function scheduleHistory(){if(disposed||historyFrame)return;historyFrame=requestAnimationFrame(()=>{historyFrame=0;const box=boundary.getBoundingClientRect(),viewport=history.getBoundingClientRect();if(root.isConnected&&history.clientHeight&&box.bottom>=viewport.top&&box.top<=viewport.bottom)loadEarlier();});}
  // Commit loading chrome through the list before the domain starts its read.
  function showHistoryStatus(){boundary.replaceChildren();if(historyBusy)boundary.textContent=t('history.loading');else if(historyFailure){boundary.append(n('span','',t(historyFailure.storage?'storage.failed':'tasks.historyFailed')),action(t('retry'),()=>{historyFailure=null;historyAttempt='';loadEarlier();}));}renderTranscript();}
  async function loadEarlier(){
    const before=state?.messages.find(message=>!message.context_only)?.sequence,key=historyRun+':'+before;
    if(!onEarlier||!state?.has_more||!before||historyBusy||historyFailure||historyAttempt===key||disposed)return;
    historyAttempt=key;historyBusy=true;historyAbort=new AbortController();const controller=historyAbort;
    if(transcriptWindow.following)transcriptWindow.pause();showHistoryStatus();
    try{await onEarlier({before,signal:controller.signal});}
    catch(reason){if(!disposed&&!controller.signal.aborted){historyFailure=reason;}}
    finally{if(historyAbort===controller){historyBusy=false;historyAbort=null;if(!disposed){showHistoryStatus();scheduleHistory();}}}
  }
  latest.onclick=async()=>{historyAbort?.abort();historyAbort=null;historyBusy=false;historyFailure=null;historyAttempt='';showHistoryStatus();try{await onLatest?.();if(!disposed){transcriptWindow.scrollToLatest();renderTranscript();}}catch(reason){if(!disposed){historyFailure=reason;showHistoryStatus();}}};
  async function run(work){
    if(busy||disposed)return;busy=true;error.hidden=true;updateComposer();renderMenu();
    try{await work();if(!disposed){dialog.close();error.textContent='';}}
    catch(reason){if(!disposed){error.textContent=reason.code==='task_disposition_conflict'?t('tasks.dispositionConflict'):agentFailureKey(reason.code||reason.message)?t(agentFailureKey(reason.code||reason.message)):reason.message||String(reason);error.hidden=false;if(dialog.open)dialog.querySelector('.task-menu-error')?.replaceChildren(n('span','',error.textContent));}}
    finally{busy=false;if(!disposed){updateComposer();renderMenu();}}
  }
  async function submit(){
    if(!fresh||!can('task.input')||busy||editor.composing||!completion.value().trim()||connectionError)return;
    const text=completion.value(),version=draftVersion;
    await run(async()=>{await onSend(text);if(disposed)return;if(completion.value()===text&&version===draftVersion){completion.setValue('');onDraft('');}completion.reset();});
  }
  form.onsubmit=event=>{event.preventDefault();if(!editor.mobile)submit();};send.onclick=()=>completion.value().trim()?submit():run(onStop);
  function updateComposer(){return updateComposerLayout();}
  function updateComposerLayout(){
    const running=active(),hasText=!!completion.value().trim(),stop=running&&!hasText;
    form.hidden=!can('task.input')&&!can('task.interrupt');input.readOnly=!can('task.input');input.setAttribute('aria-label',t('tasks.followupLabel'));input.placeholder=t('tasks.followupPlaceholder');
    completion.refresh();
    send.hidden=false;send.replaceChildren(icon(stop?'stop':'send'));label(send,stop?tr('Stop task','停止任务'):t('tasks.sendFollowup'));
    send.disabled=!fresh||busy||(!running&&!hasText)||!!connectionError||(stop?!can('task.interrupt'):!can('task.input'));retry.disabled=busy;
    footer.hidden=running||!can('task.input');footer.textContent=t(record.info?.archived?'tasks.archivedHint':'tasks.followupHint');
    if(!running&&record.info?.archive_reason)footer.textContent+=' · '+record.info.archive_reason;
  }
  function renderStage(){
    const rawPhase=record.info?.phase||task.phase,pending=approvals.length,phase=rawPhase;
    stage.dataset.phase=unavailable||connectionError?'offline':pending?'approval':record.info?.paused?'paused':phase;
    const text=storageError?t('storage.failed'):connectionError?tr('Connection interrupted · Showing retained history','连接中断 · 显示已有记录'):unavailable?tr('Original machine is offline · Showing cached output','原执行机器离线 · 显示缓存执行输出'):!fresh?t('tasks.cachedState'):pending?tr(`Waiting for approval · ${pending} pending`,`等待授权 · ${pending} 项待处理`):record.info?.paused?tr('Paused','已暂停'):t(phase==='waiting'?'tasks.stage.waiting':`schedule.state.${phase}`);
    stageInner.querySelector('.agent-source')?.remove();const source=taskAgentSource({...task,agent_link:linked()},locale());if(source)stageInner.prepend(source);
    if(stageLabel.textContent!==text)stageLabel.textContent=text;stageLabel.title=connectionError;locate.hidden=!pending||!!connectionError;retry.hidden=!connectionError||!can('task.read');retry.disabled=busy;
  }
  function locateApproval(){
    for(const message of projectTaskMessages(state,inputs,record))if(message.tool&&approvalFor(message.tool)){
      let parent=message;while(parent?.tool){const key=JSON.stringify([scope,task.id,parent.id]);toolMemory.set(key,{...toolMemory.get(key),open:true});parent=forest?.byID.get(parent.tool.turn_id+':'+parent.tool.parent_id)?.message;}
      transcriptWindow.locate({id:message.id,expand:true});renderTranscript();
      transcript.querySelector('.tool-call[data-phase="waiting_approval"] summary')?.focus({preventScroll:true});return;
    }
    if(approvalHost.querySelector('.approval-card')){transcriptWindow.locate({key:'task-approvals'});renderTranscript();approvalHost.querySelector('button')?.focus({preventScroll:true});}
  }
  approvalHost.dataset.scrollAnchor='task-approvals';
  function copyButton(entry){
    if(entry.copy){label(entry.copy,t(entry.copy.dataset.copied?'copied':'copy'));return;}entry.copy=iconButton('copy',t('copy'),'copy-message');copyFeedback(entry.copy,{text:()=>entry.text||'',locale,translate,onError:reason=>{error.textContent=reason.message;error.hidden=false;}});entry.element.append(entry.copy);
  }
  const disposeMessage=entry=>{entry.childrenWindow?.close();entry.element.remove();};
  function renderMessage(message,existing){
      let entry=existing;
      if(!entry){const element=n('article',`message task-event role-${message.role}`);entry={element,row:element,text:null};}
      entry.element.dataset.messageId=message.id;entry.element.dataset.scrollAnchor=`message:${message.id}`;
      if(message.compaction){
 if(!entry.compact){entry.compact=createCompaction({locale});entry.element.append(entry.compact.element);}
 entry.compact.update(message);
 }else if(message.tool){
        if(!entry.tool){entry.tool=createToolCall({messageActor:()=>record.info?.address||task.address,onOpenTask,resolveMessageIdentity:address=>messageIdentities(client).resolve(address),memory:toolMemory,key:JSON.stringify([scope,task.id,message.id]),locale,machineLabel,onDecision,onInteract:event=>transcriptWindow.engage(event),onLocate:(call,turn)=>{const target=forest.byID.get(turn+':'+call)?.message;if(target){transcriptWindow.locate({id:target.id,expand:true});renderTranscript();}},downloadFile,acquireFileImage});entry.element.append(entry.tool.element);}
        const children=forest.byID.get(message.tool.turn_id+':'+message.tool.id)?.children||[];
        const approvalDescendant=children.some(function pending(entry){return !!approvalFor(entry.message.tool)||entry.children.some(pending);});
        entry.tool.update(message,{stacked:message.communicationStacked,approval:approvalFor(message.tool),approvalDescendant,offline:unavailable||!!connectionError,children:children.map(child=>child.message)});
        if(transcriptWindow.revealing&&transcriptWindow.visibilityPath.has(message.id)||transcriptWindow.destination?.expand&&transcriptWindow.destination.id===message.id)entry.tool.element.open=true;
        if(children.length||entry.childrenWindow){
          if(!entry.childrenWindow){entry.childrenWindow=createCommunicationWindow({onOpenTask,locale,actor:()=>record.info?.address||task.address,resolveIdentity:address=>messageIdentities(client).resolve(address),blocked:message=>!!approvalFor(message.tool),memory:toolMemory,scope:JSON.stringify([scope,task.id]),scroller:history,host:entry.tool.children,nested:true,render:renderMessage,dispose:disposeMessage,requestRender:scheduleFollow,controller:transcriptWindow,heights});entry.tool.element.addEventListener('toggle',scheduleFollow);}
          entry.childrenWindow.setMessages(entry.tool.element.open?children.map(child=>({...child.message,viewKey:child.message.id})):[]);
        }
      }else{
        if(!entry.body){entry.body=n('div','message-body');entry.element.append(entry.body);if(message.role==='assistant')entry.reply=createReplyBubble(entry.body);}
        if(message.role==='assistant')entry.reply.update(message,{t,downloadFile,acquireFileImage,...resourceOptions,onSettings:can('assistant.configure')?onSettings:undefined});
        else{
          if(!entry.source){entry.source=n('div','task-message-source');entry.element.prepend(entry.source);}
          const sender=message.input?.sender,name=sender===task.assistant_id?assistantName:(client.assistants||[]).find(item=>item.id===sender&&item.space_id===scope.space)?.name||sender;
          entry.source.textContent=message.role==='user'?tr('You · Guidance','你 · 指导'):`${message.input?.kind==='reference'?tr('Reference','参考信息'):tr('Guidance','指导')}${name?' · '+name:''}`;
          if(entry.text!==message.text)entry.body.replaceChildren(renderMarkdown(message.text,{downloadFile,acquireFileImage}));
          if(!entry.delivery){entry.delivery=n('span','task-input-delivery');entry.element.append(entry.delivery);}
          const status=message.input?.state;entry.delivery.hidden=status==='consumed';entry.delivery.textContent=tr({pending:'Pending delivery',retained:'Retained in history',cancelled:'Cancelled'}[status]||'',{pending:'待送达',retained:'已保留在记录中',cancelled:'已取消'}[status]||'');
        }
        entry.text=message.text;if(message.text)copyButton(entry);
      }
      return entry;
  }
  function renderTranscript(){
    if(!state)return;
    if(!projection){const rows=projectScriptRuns(projectTaskMessages(state,inputs,record,{offline:unavailable||!!connectionError}),{activeTurn:active()?[...state.tools.values()].at(-1)?.tool.turn_id:null});projection={rows,forest:toolForest(rows)};}
    const {rows}=projection;forest=projection.forest;
    const parents=new Map(rows.flatMap(message=>{const parent=message.tool?.parent_id&&forest.byID.get(message.tool.turn_id+':'+message.tool.parent_id)?.message;return parent?[[message.id,parent.id]]:[];}));
    transcriptWindow.setMessages(rows.filter(message=>!message.tool?.parent_id||!forest.byID.has(message.tool.turn_id+':'+message.tool.parent_id)).map(message=>({...message,viewKey:message.id})),{parents});
    latest.hidden=transcriptWindow.following;scheduleHistory();
  }
  function renderMenu(force=false){
    if(!dialog.open&&!force)return;
    const signature=JSON.stringify([menuMode,fresh,record.info?.phase,record.info?.paused,record.info?.archived,record.info?.started_at_ms,record.info?.finished_at_ms,record.capabilities,linked(),machineLabel(record.info?.machine_id||task.machine_id),busy,locale()]);if(!force&&signature===menuSignature)return;menuSignature=signature;
    const heading=n('div','task-menu-heading'),close=iconButton('close',t('close'));close.onclick=()=>dialog.close();heading.append(n('h2','',tr('Task actions','任务操作')),close);dialog.replaceChildren(heading);
    const choice=(text,work,{danger=false}={})=>{const button=action(text,work,{danger});button.classList.add('menu-destination');button.disabled=busy;dialog.append(button);return button;};
    if(menuMode==='details'){
      heading.firstChild.textContent=tr('Task details','任务详情');const data=n('dl','task-info');
      for(const [key,value]of [[tr('Assistant','助手'),assistantName],[t('agentLinks.executionSource'),linked()?linkDisplayName({peer:linked()}):null],[t('schedule.machine'),linked()?null:machineLabel(record.info?.machine_id||task.machine_id)],[tr('Model','模型'),linked()?null:record.config?.model],[tr('Started','开始时间'),record.info?.started_at_ms?new Date(record.info.started_at_ms).toLocaleString(locale()):null],[t('tasks.elapsedLabel'),['complete','failed','cancelled','interrupted'].includes(record.info?.phase)?taskElapsed(record.info?.started_at_ms,taskClockEnd(record.info),locale(),true):null],[t('tasks.taskIdentity'),taskAddress(record.info||task)?JSON.stringify(taskAddress(record.info||task)):task.id]])if(value)data.append(n('dt','',key),n('dd','',value));
      dialog.append(data);choice(tr('Back','返回'),()=>{menuMode='actions';renderMenu(true);});return;
    }
    if(menuMode==='cancel'){
      dialog.append(n('p','',tr('Stop this task and retain its history?','取消任务并保留已有记录？')));choice(tr('Keep task','保留任务'),()=>{menuMode='actions';renderMenu(true);});choice(tr('Cancel task','取消任务'),()=>run(onCancel),{danger:true});
    }else{
      if(fresh&&can('task.control')&&['running','waiting_approval'].includes(record.info?.phase))choice(record.info?.paused?tr('Resume task','恢复任务'):tr('Pause task','暂停任务'),()=>run(onPause));
      if(fresh&&!active()&&can('task.archive'))choice(t(record.info?.archived?'tasks.unarchive':'tasks.archive'),()=>run(onArchive));
      if(can('assistant.usage'))choice(tr('Usage','用量'),()=>{dialog.close();onUsage();});choice(tr('Task details','任务详情'),()=>{menuMode='details';renderMenu(true);});
      if(fresh&&can('task.cancel')&&!['complete','cancelled'].includes(record.info?.phase))choice(tr('Cancel task','取消任务'),()=>{menuMode='cancel';renderMenu(true);},{danger:true});
    }
    dialog.append(n('p','inline-error task-menu-error',error.hidden?'':error.textContent));
  }
  const onToggle=()=>scheduleFollow();goal.addEventListener('toggle',onToggle);
  updateIdentity();updateComposer();renderStage();
  return {
    transcript,history,input,approvalHost,
    update(next){
      if(disposed)return;
      projection=null;
      if(next.storageError!==undefined)storageError=next.storageError;if(next.fresh!==undefined)fresh=next.fresh;historyNotice.hidden=!(next.state?.cache_truncated||next.record?.cache_truncated||next.cacheTruncated);historyNotice.textContent=t('tasks.cachedOutputBounded');const nextRun=next.record?.run_id||task.id;if(nextRun!==historyRun){if(historyRun)transcriptWindow.reset();historyAbort?.abort();historyAbort=null;historyBusy=false;historyFailure=null;historyAttempt='';historyRun=nextRun;showHistoryStatus();}if(next.record)record=next.record;messageIdentities(client).rememberTasks([record.info||task]);if(next.state)state=next.state;if(next.inputs)inputs=next.inputs;if(next.approvals)approvals=next.approvals;if(next.unavailable!==undefined)unavailable=next.unavailable;if(next.connectionError!==undefined)connectionError=next.connectionError;
      updateIdentity();renderStage();renderTranscript();scheduleFollow();updateComposer();renderMenu();
      renderDownloadFailure();
      const attachments=record.outcome?.artifacts?.map(path=>({path}))||record.files||[],artifactError=record.outcome?.artifacts_error||'';
      const signature=JSON.stringify([attachments,artifactError,locale()]);if(fileSignature!==signature){
        fileSignature=signature;files.replaceChildren();
        for(const file of attachments){const link=action(file.name||file.path,()=>downloadFile({task:task.id,path:file.path}));files.append(link);}
        if(artifactError){const warning=n('div','task-artifacts-error');warning.append(n('p','hint',t('tasks.artifactsError')));const details=n('details');details.append(n('summary','',t('tasks.artifactsErrorDetails')),n('pre','',artifactError));warning.append(details);files.append(warning);}
      }
    },
    openMenu(){menuMode='actions';menuSignature='';renderMenu(true);dialog.showModal();},
    position(){return transcriptWindow.snapshot();},
    restore(position){transcriptWindow.restore(position);editor.resize();renderTranscript();},
    start(){editor.resize();renderTranscript();},
    dispose(){disposed=true;historyAbort?.abort();cancelAnimationFrame(historyFrame);cancelAnimationFrame(frame);cancelAnimationFrame(resizeFrame);stopReading();transcriptWindow.close();resizeObserver.disconnect();completion.dispose();editor.destroy();goal.removeEventListener('toggle',onToggle);dialog.close();dialog.remove();},
  };
}
