import {activeTool,toolResult,toolSummary,toolFailure,patchFiles} from '../state/tool-calls.mjs';
import {translate} from '../locales/messages.mjs';
import {renderMarkdown} from './markdown.mjs';
import {createApprovalCard} from './approval-card.mjs';
const n=(tag,cls='',text)=>{const node=document.createElement(tag);node.className=cls;if(text!==undefined)node.textContent=text;return node;};
const pretty=value=>typeof value==='string'?value:JSON.stringify(value??null,null,2);
const LIMIT=24000;
const clocks=new Set();let clockFrame=0,lastTick=0;
function scheduleClock(update){clocks.add(update);if(clockFrame)return;const frame=now=>{clockFrame=0;if(now-lastTick>=1000){lastTick=now;for(const tick of clocks)if(!tick())clocks.delete(tick);}if(clocks.size)clockFrame=requestAnimationFrame(frame);};clockFrame=requestAnimationFrame(frame);}


// Stable nodes preserve native disclosure, selection and output scroll on updates.
export function createToolCall({memory=new Map(),key='',locale=()=> 'en',onDecision,downloadFile,acquireFileImage}={}){
  const t=name=>translate(locale(),`tool.${name}`),root=n('details','tool-call'),summary=n('summary','tool-summary');
  root.dataset.cacheKey=`tool:${key}`;
  const title=n('span','tool-title'),excerpt=n('span','tool-excerpt'),status=n('span','tool-status'),duration=n('span','tool-duration');summary.append(title,excerpt,status,duration);
  const context=n('div','tool-context'),failure=n('p','tool-failure'),body=n('div','tool-body'),metadata=n('dl','tool-metadata'),approval=n('div','tool-approval'),history=n('div','tool-history-label'),children=n('div','tool-children'),sections=n('div'),raw=n('details','tool-raw'),rawTitle=n('summary'),rawBody=n('div');raw.append(rawTitle,rawBody);body.append(metadata,approval,history,children,sections,raw);summary.append(context,failure);root.append(summary,body);history.hidden=true;
  let current,options={},signature='',approvalID='',approvalCard=null,rawSignature='',autoOpened=false,building=false;
  const sectionsByKey=new Map();
  root.open=memory.get(key)?.open||false;raw.open=memory.get(key)?.raw||false;
  const remember=()=>{if(!building)memory.set(key,{...memory.get(key),open:root.open,raw:raw.open});};root.addEventListener('toggle',remember);raw.addEventListener('toggle',remember);
  function copyButton(label,get){const button=n('button','tool-action',label);button.type='button';button.onclick=async()=>{try{await navigator.clipboard.writeText(get());button.textContent=t('copied');}catch{button.textContent=t('copyFailed');}};return button;}
  function section(id,label,value,{output=false,markdown=false}={}){
    let item=sectionsByKey.get(id);
    if(!item){const box=n('section','tool-section'),head=n('div','tool-section-heading'),heading=n('strong'),pre=n('pre','tool-code');const copy=copyButton(t('copy'),()=>item.value);head.append(heading,copy);let follow;
      if(output){follow=n('button','tool-action');follow.type='button';head.append(follow);}
      const truncated=n('p','tool-truncated');box.append(head,pre,truncated);item={box,heading,copy,pre,truncated,value:'',following:true,follow};sectionsByKey.set(id,item);sections.append(box);
      if(output){pre.addEventListener('scroll',()=>{item.following=pre.scrollHeight-pre.clientHeight-pre.scrollTop<24;follow.textContent=t(item.following?'following':'latest');});follow.onclick=()=>{item.following=true;update(current,options);pre.scrollTop=pre.scrollHeight;follow.textContent=t('following');};}
    }
    item.heading.textContent=label;item.copy.textContent=t('copy');item.box.hidden=false;
    const text=pretty(value);item.value=text;
    const visible=output?(item.following?text.slice(-LIMIT):(text.length>LIMIT?item.displayed??text.slice(0,LIMIT):text)):text.slice(0,LIMIT);
    if(item.displayed!==visible){const top=item.pre.scrollTop;item.displayed=visible;if(markdown)item.pre.replaceChildren(renderMarkdown(visible,{downloadFile,acquireFileImage}));else item.pre.textContent=visible;if(output&&item.following)item.pre.scrollTop=item.pre.scrollHeight;else item.pre.scrollTop=top;}

    item.truncated.hidden=text.length<=LIMIT;item.truncated.textContent=t('displayTruncated');
    if(item.follow)item.follow.textContent=t(item.following?'following':'latest');
    return item;
  }
  function update(message,next={}){
    current=message;options=next;const tool=message.tool;if(!tool)return;
    const result=toolResult(tool),pending=next.approval,script=tool.script,isGroup=tool.name==='script_group';
    root.classList.toggle('script-group',isGroup);root.classList.toggle('script-run',!!script&&!isGroup);
    metadata.hidden=isGroup;raw.hidden=isGroup;history.hidden=!isGroup;history.textContent=t('executionHistory');

    const phase=pending?'waiting_approval':tool.phase;
    const names={exec_command:'command',write_stdin:'session',apply_patch:'patch',exec:'script',wait:'wait',cua_repl:'computer',create_task:'createTask',get_task:'getTask',wait_task:'waitTask'};
    title.textContent=tool.name==='read_chat_messages'?translate(locale(),'references.read'):names[tool.name]?t(names[tool.name]):tool.name;
    const preview=toolSummary(tool);
    excerpt.textContent=preview===tool.name||preview===title.textContent?'':preview;excerpt.title=excerpt.textContent;excerpt.hidden=!excerpt.textContent;
    let shown=phase;
    if(phase==='completed'&&result?.session_id)shown='sessionReturned';
    if(phase==='completed'&&result?.completed===false&&result?.exec_id&&!script)shown='scriptYielded';
    const childTools=(next.children||[]).map(child=>child.tool);
    status.textContent=t(next.offline&&activeTool(tool)?'reconnecting':shown);
    if(childTools.length&&!script){const complete=childTools.filter(child=>!activeTool(child)).length;if(complete<childTools.length&&!next.offline)status.textContent=t(childTools.filter(activeTool).every(child=>child.phase==='waiting_approval')?'waiting_approval':'scriptRunning');status.textContent+=` · ${complete}/${childTools.length}`;}
    if(script){
      const focus=script.focus,label=focus?.name==='exec_command'?focus.args?.cmd?.trim().split('\n')[0]:focus?(names[focus.name]?t(names[focus.name]):focus.name):'';
      title.textContent=isGroup&&phase==='completed'?t('scriptCompleted'):label||t(isGroup&&activeTool(tool)?'scriptRunning':'script');
      excerpt.hidden=true;excerpt.textContent='';title.title=title.textContent;
      if(isGroup&&tool.failurePhase&&['failed','denied'].includes(tool.failurePhase)&&!activeTool(tool))status.textContent=t('hasFailures');
    }
    root.dataset.phase=phase;root.dataset.callId=tool.id;root.dataset.turnId=tool.turn_id||'';
    const start=script?tool.started_at_ms:phase==='waiting_approval'?(tool.waiting_at_ms||tool.started_at_ms):(tool.running_at_ms||tool.started_at_ms);
    // Live clocks run only while a visible operation needs one, and honor backend time.
    const elapsed=start?Math.max(0,((tool.finished_at_ms||Date.now())-start)/1000):null;
    duration.textContent=elapsed===null?'':`${phase==='waiting_approval'&&!script?t('waited')+' ':''}${elapsed<1?elapsed.toFixed(1):Math.floor(elapsed)}s`;
    context.textContent=[tool.target?.runtime_id,tool.workdir,tool.execution_mode==='host'?t('hostExecution'):tool.execution_mode==='sandbox'?t('sandboxExecution'):''].filter(Boolean).join(' · ');context.hidden=!activeTool(tool)||!context.textContent;
    const error=tool.diagnostic||(tool.failurePhase?t(tool.failurePhase):['failed','denied','unknown'].includes(phase)?String(toolFailure(tool)||t(phase)).split('\n')[0]:'');failure.textContent=String(error).split('\n')[0];failure.hidden=!error;
    failure.onclick=script?event=>{event.preventDefault();root.open=true;queueMicrotask(()=>{const target=[...root.querySelectorAll('.tool-call')].find(node=>node.dataset.callId===(tool.failureID||script.failureID));if(target){for(let parent=target;parent&&parent!==root;parent=parent.parentElement)if(parent.tagName==='DETAILS')parent.open=true;target.scrollIntoView({block:'nearest'});}});}:null;
    if(script&&error){failure.tabIndex=0;failure.setAttribute('role','button');failure.onkeydown=event=>{if(['Enter',' '].includes(event.key)){event.preventDefault();failure.click();}};}else{failure.removeAttribute('tabindex');failure.removeAttribute('role');failure.onkeydown=null;}
    const nextSignature=JSON.stringify([tool.name,tool.target,tool.workdir,tool.execution_mode,locale()]);
    if(signature!==nextSignature){signature=nextSignature;metadata.replaceChildren();for(const [label,value]of [[t('executionMode'),tool.execution_mode==='host'?t('hostExecution'):tool.execution_mode==='sandbox'?t('sandboxExecution'):null],[t('name'),tool.name],[t('target'),tool.target?.runtime_id],[t('workdir'),tool.workdir]])if(value)metadata.append(n('dt','',label),n('dd','',value));}
    if(pending){
      if(approvalID!==pending.id){approvalID=pending.id;approvalCard=createApprovalCard({locale,onDecision});approval.replaceChildren(approvalCard.element);}
      approvalCard.update(pending);
    }else{approvalID='';approvalCard=null;approval.replaceChildren();}
    approval.hidden=!pending;
    if(pending&&!autoOpened){building=true;root.open=true;autoOpened=true;building=false;remember();queueMicrotask(()=>{for(let parent=root.parentElement?.closest('details.tool-call');parent;parent=parent.parentElement?.closest('details.tool-call'))parent.open=true;});}
    if(!pending)autoOpened=false;
    if(activeTool(tool))scheduleClock(tick);
    for(const item of sectionsByKey.values())item.box.hidden=true;
    if(isGroup)return;
    const args=tool.args||{};
    if(pending){/* The exact approval arguments above are the operation preview. */}
    else if(tool.name==='exec_command')section('input',t('command'),args.cmd||'');
    else if(['exec','cua_repl'].includes(tool.name))section('input',t('code'),args.code||'');
    else if(tool.name==='apply_patch'){
      const patch=args.patch||'';const files=patchFiles(patch);if(files.length)section('files',t('files'),files.map(file=>`${file.path} · ${t(file.operation)}`).join('\n'));
      const item=section('input',t('changes'),patch||pretty(args));
      if(item.patch!==patch){item.patch=patch;item.pre.replaceChildren();for(const line of patch.slice(0,LIMIT).split('\n'))item.pre.append(n('span',line.startsWith('+')?'tool-diff-add':line.startsWith('-')?'tool-diff-remove':'',line+'\n'));}
    }
    else section('input',t('arguments'),args);
    const output=(tool.output||[]).map(chunk=>`${chunk.stream?`[${chunk.stream}]\n`:''}${chunk.text}`).join('\n');
    if(output)section('output',t('output'),output,{output:true});
    else if(activeTool(tool)&&!pending&&phase!=='waiting_approval'&&!['exec','wait'].includes(tool.name))section('output',t('output'),t('noOutput'),{output:true});
    if(result!==undefined){
      if(result&&typeof result==='object'&&('stdout'in result||'stderr'in result)){
        if(!output)section('output',t('output'),[result.stdout,result.stderr?`[stderr]\n${result.stderr}`:''].filter(Boolean).join('\n')||t('emptyOutput'),{output:true});
        const info=[result.exit_code!=null?`${t('exitCode')} ${result.exit_code}`:'',result.session_id?`${t('sessionID')} ${result.session_id}`:'',result.truncated?t('sourceTruncated'):''].filter(Boolean).join(' · ');section('result',t('result'),info||result);
      }else if(['exec','wait'].includes(tool.name)&&typeof result?.output==='string'){
        section('result',t('scriptOutput'),script?.output||result.output||t('emptyOutput'),{output:true});
      }else if(Array.isArray(result?.content)){
        const texts=result.content.filter(block=>block.type==='text').map(block=>block.text).join('\n');section('result',t('result'),texts||result,{markdown:!!texts});
        let media=sectionsByKey.get('media');if(!media){const box=n('div','tool-media');media={box};sectionsByKey.set('media',media);sections.append(box);}media.box.hidden=false;
        const images=result.content.filter(block=>block.type==='image'&&/^image\/(png|jpeg|webp|gif)$/.test(block.mimeType)&&typeof block.data==='string');
        if(media.result!==result){media.result=result;media.box.replaceChildren();for(const block of images){const img=n('img');img.alt=t('image');img.loading='lazy';img.src=`data:${block.mimeType};base64,${block.data}`;media.box.append(img);}}
      }else section('result',t('result'),result);
    }
    rawTitle.textContent=t('raw');const calls=script?.calls||[tool],rawValue=JSON.stringify([locale(),calls]);
    if(rawSignature!==rawValue){rawSignature=rawValue;rawBody.replaceChildren();for(const call of calls){rawBody.append(n('p','tool-call-id',script?`${call.name} · ${call.id}`:call.id),n('p','tool-call-time',call.started_at_ms?new Date(call.started_at_ms).toLocaleString(locale()):''));for(const [label,value]of [[t('arguments'),call.args],[t('result'),call.result]])if(value!==undefined){const text=pretty(value),head=n('div','tool-section-heading');head.append(n('strong','',label),copyButton(t('copyJSON'),()=>text));rawBody.append(head,n('pre','tool-code',text.slice(0,LIMIT)));if(text.length>LIMIT)rawBody.append(n('p','tool-truncated',t('displayTruncated')));}}}
  }
  function tick(){if(!root.isConnected||!activeTool(current?.tool))return false;if(document.hidden)return true;const tool=current.tool,waiting=options.approval||tool.phase==='waiting_approval',start=tool.script?tool.started_at_ms:waiting?(tool.waiting_at_ms||tool.started_at_ms):tool.running_at_ms||tool.started_at_ms;if(start)duration.textContent=`${waiting&&!tool.script?t('waited')+' ':''}${Math.max(0,Math.floor((Date.now()-start)/1000))}s`;return true;}
  return {element:root,children,update};
}
