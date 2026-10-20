import {createCompaction} from './compaction.mjs';
import {taskAgentSource} from './connection-visuals.mjs';
import {agentFailureKey} from '../state/agent-links.mjs';
import {node as n,action} from './primitives.mjs';
import {icon} from './icons.mjs';
import {translate} from '../locales/messages.mjs';
import {bindComposer} from './composer.mjs';
import {bindCompletions} from './completions.mjs';
import {createReplyBubble} from './reply.mjs';
import {createToolCall} from './tool-call.mjs';
import {renderMarkdown} from './markdown.mjs';
import {toolForest} from '../state/tool-calls.mjs';
import {projectScriptRuns} from '../state/script-runs.mjs';
import {projectTaskMessages} from '../state/task-transcript.mjs';
import {createMessageWindow,readingAnchor,restoreReadingAnchor} from './message-window.mjs';
import {copyFeedback} from './motion.mjs';

export function createTaskConversation({root,task,record:initial,locale,client,scope,draft,onDraft,downloadFile,toolMemory,approvalFor,onDecision,onSend,onStop,onPause,onCancel,onArchive,onUsage,onRefresh,onSettings,assistantName}){
  const acquireFileImage=client.acquireFileImage?({task,path})=>client.acquireFileImage(task,path,scope):undefined;
  const resourceOptions={acquireResource:client.acquireResource?resource=>client.acquireResource(resource,scope):undefined,downloadResource:client.downloadResource?resource=>client.downloadResource(resource,scope):undefined};
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en,t=key=>translate(locale(),key);
  const label=(button,text)=>{button.title=text;button.setAttribute('aria-label',text);};
  const iconButton=(name,text,className='icon-button')=>{const button=action('',()=>{},{});button.className=className;button.append(icon(name));label(button,text);return button;};
  const stage=n('div','task-stage'),stageInner=n('div','task-stage-inner'),stageLabel=n('span','task-status'),locate=action(tr('View approval','查看授权'),locateApproval),retry=action(tr('Retry','重试'),()=>run(onRefresh));
  stage.setAttribute('role','status');stageInner.append(stageLabel,locate,retry);stage.append(stageInner);
  const history=n('div','task-history'),column=n('div','task-column'),goal=n('details','task-goal'),goalSummary=n('summary'),goalPreview=n('span','task-goal-preview'),goalBody=n('div','message-body task-goal-body');
  history.dataset.scrollKey='task-history';goal.dataset.cacheKey='task-goal';goalSummary.append(icon('chevron-right'),n('span','task-goal-label',tr('Task goal','任务目标')),goalPreview);goal.append(goalSummary,goalBody);
  goalPreview.textContent=(initial.request?.text||'').replace(/\s+/g,' ');goalBody.append(renderMarkdown(initial.request?.text||'',{downloadFile,acquireFileImage}));
  const transcript=n('div','task-transcript'),files=n('div','task-files');transcript.setAttribute('role','log');transcript.setAttribute('aria-label',tr('Task conversation','任务对话'));
  const approvalHost=n('div','task-unmatched-approvals');column.append(goal,approvalHost,transcript,files);history.append(column);
  const composerArea=n('div','composer-area task-composer-area'),form=n('form','composer'),input=n('textarea','composer-input'),send=iconButton('stop',tr('Stop task','停止任务'),'send-button'),latest=iconButton('down',t('latest'),'latest-button'),footer=n('p','task-ended'),error=n('p','task-action-error');
  input.rows=1;input.id=`task-composer-${task.id}`;input.setAttribute('autocapitalize','sentences');input.setAttribute('aria-label',t('tasks.followupLabel'));input.placeholder=t('tasks.followupPlaceholder');
  input.value=draft;form.append(input,send);composerArea.append(latest,form,footer,error);latest.hidden=true;footer.hidden=true;error.hidden=true;error.setAttribute('role','alert');
  root.classList.add('task-conversation');root.append(stage,history,composerArea);
  const dialog=n('dialog','preferences product-actions task-actions');dialog.setAttribute('aria-label',tr('Task actions','任务操作'));root.append(dialog);
  let record=initial,state,inputs=[],approvals=[],unavailable=false,connectionError='',busy=false,disposed=false,following=true,pendingFollow=false,restored=false,frame=0,menuMode='actions',menuSignature='',fileSignature='',draftVersion=0;
  let forest,reading=null;
  const editor=bindComposer(input,{onInput:text=>{draftVersion++;onDraft(text);updateComposer();},onSend:submit,onCompositionEnd:updateComposer});
  const completion=bindCompletions(input,{client:{scope:()=>scope,request:(path,options)=>client.request(path,options)},host:composerArea,locale,mobile:editor.mobile,id:`pot-task-${task.id}-completions`});
  const linked=()=>record.info?.agent_link||task.agent_link;
  const active=()=>['queued','running','waiting','waiting_approval'].includes(record.info?.phase||task.phase);
  const nearBottom=()=>history.scrollHeight-history.clientHeight-history.scrollTop<64;
  function scrollBottom(){history.scrollTop=history.scrollHeight;following=true;latest.hidden=true;}
  function scheduleFollow(){if(frame||disposed)return;frame=requestAnimationFrame(()=>{frame=0;if(disposed)return;const follow=following||pendingFollow;renderTranscript();if(follow){scrollBottom();renderTranscript();scrollBottom();}else{restoreReadingAnchor(history,reading);latest.hidden=nearBottom();}pendingFollow=false;reading=readingAnchor(history);});}
  let previousWidth=0,resizeFrame=0;
  const resizeObserver=new ResizeObserver(()=>{if(root.clientWidth!==previousWidth){previousWidth=root.clientWidth;cancelAnimationFrame(resizeFrame);resizeFrame=requestAnimationFrame(()=>{if(!disposed)editor.resize();});}scheduleFollow();});resizeObserver.observe(column);resizeObserver.observe(composerArea);
  history.addEventListener('scroll',()=>{if(!pendingFollow)following=nearBottom();latest.hidden=following;reading=readingAnchor(history);scheduleFollow();},{passive:true});latest.onclick=()=>{scrollBottom();scheduleFollow();};
  const pauseFollow=()=>{following=false;pendingFollow=false;};
  history.addEventListener('wheel',event=>{if(event.deltaY<0)pauseFollow();},{passive:true});
  let touchY;history.addEventListener('touchstart',event=>{touchY=event.touches[0]?.clientY;},{passive:true});
  history.addEventListener('touchmove',event=>{const y=event.touches[0]?.clientY;if(y>touchY)pauseFollow();touchY=y;},{passive:true});
  async function run(work){
    if(busy||disposed)return;busy=true;error.hidden=true;updateComposer();renderMenu();
    try{await work();if(!disposed){dialog.close();error.textContent='';}}
    catch(reason){if(!disposed){error.textContent=reason.code==='task_disposition_conflict'?t('tasks.dispositionConflict'):agentFailureKey(reason.code||reason.message)?t(agentFailureKey(reason.code||reason.message)):reason.message||String(reason);error.hidden=false;if(dialog.open)dialog.querySelector('.task-menu-error')?.replaceChildren(n('span','',error.textContent));}}
    finally{busy=false;if(!disposed){updateComposer();renderMenu();}}
  }
  async function submit(){
    if(linked()||busy||editor.composing||!input.value.trim()||connectionError)return;
    const text=input.value,version=draftVersion;
    await run(async()=>{await onSend(text);if(disposed)return;if(input.value===text&&version===draftVersion){editor.setValue('');onDraft('');}completion.reset();});
  }
  form.onsubmit=event=>{event.preventDefault();if(!editor.mobile)submit();};send.onclick=()=>input.value.trim()?submit():run(onStop);
  function updateComposer(){
    const running=active(),hasText=!!input.value.trim(),stop=running&&!hasText;
    form.hidden=!!linked();input.readOnly=!!linked();input.setAttribute('aria-label',t('tasks.followupLabel'));input.placeholder=t('tasks.followupPlaceholder');
    send.hidden=false;send.replaceChildren(icon(stop?'stop':'send'));label(send,stop?tr('Stop task','停止任务'):t('tasks.sendFollowup'));
    send.disabled=busy||(!running&&!hasText)||!!connectionError;retry.disabled=busy;
    footer.hidden=running;footer.textContent=t(record.info?.archived?'tasks.archivedHint':'tasks.followupHint');
    if(linked()){footer.hidden=false;footer.textContent=t('agentLinks.remoteTaskHint').replace('{name}',linked().name||t(linked().kind==='codex'?'agentLinks.sourceCodex':'agentLinks.sourcePot'));}
    if(!running&&record.info?.archive_reason)footer.textContent+=' · '+record.info.archive_reason;
  }
  function renderStage(){
    const rawPhase=record.info?.phase||task.phase,pending=approvals.length,phase=rawPhase==='waiting_approval'?'running':rawPhase;
    stage.dataset.phase=unavailable||connectionError?'offline':pending?'approval':record.info?.paused?'paused':phase;
    const text=connectionError?tr('Connection interrupted · Showing retained history','连接中断 · 显示已有记录'):unavailable?tr('Original machine is offline · Showing cached output','原执行机器离线 · 显示缓存执行输出'):pending?tr(`Waiting for approval · ${pending} pending`,`等待授权 · ${pending} 项待处理`):record.info?.paused?tr('Paused','已暂停'):tr({running:'Running',queued:'Queued',waiting:'Waiting for allowance',complete:'Complete',failed:'Failed',interrupted:'Interrupted',cancelled:'Cancelled'}[phase]||phase,{running:'运行中',queued:'排队中',waiting:'等待配额',complete:'已完成',failed:'失败',interrupted:'已中断',cancelled:'已取消'}[phase]||phase);
    stageInner.querySelector('.agent-source')?.remove();const source=taskAgentSource({...task,agent_link:linked()},locale());if(source)stageInner.prepend(source);
    if(stageLabel.textContent!==text)stageLabel.textContent=text;stageLabel.title=connectionError;locate.hidden=!pending||!!connectionError;retry.hidden=!connectionError||!!linked();retry.disabled=busy;
  }
  function locateApproval(){
    for(const message of projectTaskMessages(state,inputs,record))if(message.tool&&approvalFor(message.tool)){
      let parent=message;while(parent?.tool){const key=JSON.stringify([scope,task.id,parent.id]);toolMemory.set(key,{...toolMemory.get(key),open:true});parent=forest?.byID.get(parent.tool.turn_id+':'+parent.tool.parent_id)?.message;}
      renderTranscript();parent=message;while(parent?.tool?.parent_id){const ancestor=forest.byID.get(parent.tool.turn_id+':'+parent.tool.parent_id)?.message;if(!ancestor)break;parent=ancestor;}transcriptWindow.seek(parent.id);renderTranscript();break;
    }
    const approval=transcript.querySelector('.tool-call[data-phase="waiting_approval"]')||column.querySelector('.approval-card');
    if(!approval)return;for(let parent=approval;parent&&parent!==transcript;parent=parent.parentElement)if(parent.tagName==='DETAILS')parent.open=true;
    approval.scrollIntoView({block:'center',behavior:matchMedia('(prefers-reduced-motion: reduce)').matches?'instant':'smooth'});approval.querySelector('summary,button')?.focus({preventScroll:true});
  }
  function copyButton(entry){
    if(entry.copy){label(entry.copy,t(entry.copy.dataset.copied?'copied':'copy'));return;}entry.copy=iconButton('copy',t('copy'),'copy-message');copyFeedback(entry.copy,{text:()=>entry.text||'',locale,translate,onError:reason=>{error.textContent=reason.message;error.hidden=false;}});entry.element.append(entry.copy);
  }
  const heights=new Map();
  const disposeMessage=entry=>{entry.childrenWindow?.close();entry.element.remove();};
  function renderMessage(message,existing){
      let entry=existing;
      if(!entry){const element=n('article',`message task-event role-${message.role}`);entry={element,row:element,text:null};}
      entry.element.dataset.messageId=message.id;
      if(message.compaction){
 if(!entry.compact){entry.compact=createCompaction({locale,request:(path,options)=>client.request(path,options),scope:task.id,owner:scope});entry.element.append(entry.compact.element);}
 entry.compact.update(message);
 }else if(message.tool){
        if(!entry.tool){entry.tool=createToolCall({memory:toolMemory,key:JSON.stringify([scope,task.id,message.id]),locale,onDecision,downloadFile,acquireFileImage});entry.element.append(entry.tool.element);}
        const children=forest.byID.get(message.tool.turn_id+':'+message.tool.id)?.children||[];
        const approvalDescendant=children.some(function pending(entry){return !!approvalFor(entry.message.tool)||entry.children.some(pending);});
        entry.tool.update(message,{approval:approvalFor(message.tool),approvalDescendant,offline:unavailable||!!connectionError,children:children.map(child=>child.message)});
        if(!entry.childrenWindow){entry.childrenWindow=createMessageWindow({scroller:history,host:entry.tool.children,render:renderMessage,dispose:disposeMessage,requestRender:scheduleFollow,heights});entry.tool.element.addEventListener('toggle',scheduleFollow);}
        entry.childrenWindow.set(entry.tool.element.open?children.map(child=>({...child.message,viewKey:child.message.id})):[]);
      }else{
        if(!entry.body){entry.body=n('div','message-body');entry.element.append(entry.body);if(message.role==='assistant')entry.reply=createReplyBubble(entry.body);}
        if(message.role==='assistant')entry.reply.update(message,{t,downloadFile,acquireFileImage,...resourceOptions,onReconnect:linked()?undefined:()=>run(onRefresh),onSettings:linked()?undefined:onSettings});
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
  const transcriptWindow=createMessageWindow({scroller:history,host:transcript,render:renderMessage,dispose:disposeMessage,requestRender:scheduleFollow,heights});
  function renderTranscript(){
    if(!state)return;
    const rows=projectScriptRuns(projectTaskMessages(state,inputs,record,{offline:unavailable||!!connectionError}),{activeTurn:active()?[...state.tools.values()].at(-1)?.tool.turn_id:null});forest=toolForest(rows);
    const anchor=readingAnchor(history);
    transcriptWindow.set(rows.filter(message=>!message.tool?.parent_id||!forest.byID.has(message.tool.turn_id+':'+message.tool.parent_id)).map(message=>({...message,viewKey:message.id})),{anchor:anchor?.id});
    if(!following)restoreReadingAnchor(history,anchor);
  }
  function renderMenu(force=false){
    if(!dialog.open&&!force)return;
    const signature=JSON.stringify([menuMode,record.info?.phase,record.info?.paused,record.info?.archived,linked(),busy,locale()]);if(!force&&signature===menuSignature)return;menuSignature=signature;
    const heading=n('div','task-menu-heading'),close=iconButton('close',t('close'));close.onclick=()=>dialog.close();heading.append(n('h2','',tr('Task actions','任务操作')),close);dialog.replaceChildren(heading);
    const choice=(text,work,{danger=false}={})=>{const button=action(text,work,{danger});button.classList.add('menu-destination');button.disabled=busy;dialog.append(button);return button;};
    if(menuMode==='details'){
      heading.firstChild.textContent=tr('Task details','任务详情');const data=n('dl','task-info');
      for(const [key,value]of [[tr('Assistant','助手'),assistantName],[t('agentLinks.executionSource'),linked()?.name],[tr('Machine','执行机器'),linked()?null:record.info?.machine_id||task.machine_id],[tr('Model','模型'),linked()?null:record.config?.model],[tr('Started','开始时间'),record.info?.started_at_ms?new Date(record.info.started_at_ms).toLocaleString(locale()):null],[tr('Task ID','任务 ID'),task.id]])if(value)data.append(n('dt','',key),n('dd','',value));
      dialog.append(data);choice(tr('Back','返回'),()=>{menuMode='actions';renderMenu(true);});return;
    }
    if(menuMode==='cancel'){
      dialog.append(n('p','',tr('Stop this task and retain its history?','取消任务并保留已有记录？')));choice(tr('Keep task','保留任务'),()=>{menuMode='actions';renderMenu(true);});choice(tr('Cancel task','取消任务'),()=>run(onCancel),{danger:true});
    }else{
      if(!linked()&&['running','waiting_approval'].includes(record.info?.phase))choice(record.info?.paused?tr('Resume task','恢复任务'):tr('Pause task','暂停任务'),()=>run(onPause));
      if(!active())choice(t(record.info?.archived?'tasks.unarchive':'tasks.archive'),()=>run(onArchive));
      if(!linked())choice(tr('Usage','用量'),()=>{dialog.close();onUsage();});choice(tr('Task details','任务详情'),()=>{menuMode='details';renderMenu(true);});
      if(!['complete','cancelled'].includes(record.info?.phase))choice(tr('Cancel task','取消任务'),()=>{menuMode='cancel';renderMenu(true);},{danger:true});
    }
    dialog.append(n('p','inline-error task-menu-error',error.hidden?'':error.textContent));
  }
  const onToggle=()=>scheduleFollow();goal.addEventListener('toggle',onToggle);
  updateComposer();renderStage();
  return {
    transcript,history,input,approvalHost,
    update(next){
      if(disposed)return;
      if(next.record)record=next.record;if(next.state)state=next.state;if(next.inputs)inputs=next.inputs;if(next.approvals)approvals=next.approvals;if(next.unavailable!==undefined)unavailable=next.unavailable;if(next.connectionError!==undefined)connectionError=next.connectionError;
      pendingFollow ||= following;
      renderStage();renderTranscript();scheduleFollow();updateComposer();renderMenu();
      const signature=JSON.stringify(record.files||[]);if(fileSignature!==signature){fileSignature=signature;files.replaceChildren();for(const file of record.files||[]){const link=action(file.name||file.path,()=>downloadFile({task:task.id,path:file.path}));files.append(link);}}
    },
    openMenu(){menuMode='actions';menuSignature='';renderMenu(true);dialog.showModal();},
    restore(){restored=true;pendingFollow=false;following=nearBottom();reading=readingAnchor(history);latest.hidden=following;editor.resize();scheduleFollow();},
    start(){if(!restored)scrollBottom();editor.resize();},
    dispose(){disposed=true;cancelAnimationFrame(frame);cancelAnimationFrame(resizeFrame);transcriptWindow.close();resizeObserver.disconnect();completion.dispose();editor.destroy();goal.removeEventListener('toggle',onToggle);dialog.close();dialog.remove();},
  };
}
