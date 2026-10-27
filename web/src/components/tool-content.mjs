import {translate} from '../locales/messages.mjs';
import {activeTool} from '../state/tool-calls.mjs';
import {toolArgs,toolKind,toolLabels,patchGroups} from '../state/tool-presentation.mjs';
import {renderMarkdown} from './markdown.mjs';
const n=(tag,cls='',text)=>{const node=document.createElement(tag);node.className=cls;if(text!==undefined)node.textContent=text;return node;};
const pretty=value=>typeof value==='string'?value:JSON.stringify(value??null,null,2);
const LIMIT=24000,MAX_NODES=240;
const object=value=>value&&typeof value==='object'&&!Array.isArray(value);
const fieldKeys=new Set(['title','name','text','goal','phase','state','status','result','error','reason','action','enabled','timezone','cron','kind','at','every_minutes','path','mime_type','size','caption','mode','query','q','limit','offset','total','has_more','accepted','archived','model','profile','has_avatar','revision','address','node_id','agent_id','task_id','schedule_id','resource_id','run_id','id','url','description','items','assistants','tasks','schedules','spec','request','info','outcome','before_run','command','cwd','files','assets','target','workspace','local','agent_links','runtime_id','next_offset']);
for(const key of ['output','avatar','cancel_requested','interrupt_requested'])fieldKeys.add(key);

// Stable keyed sections keep output scrolling and selected text across snapshots.
// This view reads receipts only: its controls copy, disclose or navigate existing Tasks.
export function createToolContent({locale=()=> 'en',downloadFile,acquireFileImage,onOpenTask,resolveTask=()=>undefined}={}){
  const element=n('div','tool-content'),terminal=n('div','tool-terminal'),bar=n('div','tool-terminal-bar'),location=n('span','tool-terminal-location'),shell=n('span','tool-terminal-shell'),terminalSections=n('div'),footer=n('div','tool-terminal-footer'),sections=n('div','tool-content-sections');
  bar.append(location,shell);terminal.append(bar,terminalSections,footer);element.append(terminal,sections);
  const entries=new Map();let current,context;
  const t=key=>translate(locale(),'tool.'+key);
  const label=key=>fieldKeys.has(key)?t('field.'+key):key;
  const scalar=value=>value===null?t('valueNull'):typeof value==='boolean'?t(value?'valueYes':'valueNo'):typeof value==='number'?value.toLocaleString(locale()):String(value);
  function renderValue(value,depth=0,budget={left:MAX_NODES,chars:LIMIT},field=''){
    if(--budget.left<0||depth>5||budget.chars<=0)return n('span','tool-truncated',t('structureTruncated'));
    if(!value||typeof value!=='object'){
      let text=scalar(value);
      if(['phase','state'].includes(field)&&typeof value==='string'){const key='schedule.state.'+value,translated=translate(locale(),key);if(translated!==key)text=translated;}
      if(field==='action'&&['create','update','pause','resume','delete','run'].includes(value))text=t('action.'+value);
      if(field==='kind'&&['cron','once','interval'].includes(value))text=t('recurrence.'+value);
      const visible=text.slice(0,budget.chars);budget.chars-=text.length;return n('span','tool-value',visible);
    }
    if(Array.isArray(value)){
      if(!value.length)return n('span','tool-empty',t('emptyList'));
      const list=n('ol','tool-value-list');
      for(const item of value.slice(0,40)){const row=n('li');row.append(renderValue(item,depth+1,budget));list.append(row);if(budget.left<0||budget.chars<=0)break;}
      if(value.length>40||budget.left<0||budget.chars<=0)list.append(n('li','tool-truncated',t('structureTruncated')));
      return list;
    }
    const dl=n('dl','tool-fields'),fields=Object.entries(value).filter(([,item])=>item!==undefined);
    if(!fields.length)return n('span','tool-empty',t('emptyFields'));
    for(const [key,item]of fields.slice(0,40)){
      const name=label(key),visibleName=name.slice(0,budget.chars);budget.chars-=name.length;
      const dd=n('dd');dd.append(renderValue(item,depth+1,budget,key));dl.append(n('dt','',visibleName),dd);if(budget.left<0||budget.chars<=0)break;
    }
    if(fields.length>40||budget.left<0||budget.chars<=0)dl.append(n('dt','',t('more')),n('dd','tool-truncated',t('structureTruncated')));
    return dl;
  }
  function section(id,heading,value,{output=false,markdown=false,structured=false,parent=sections}={}){
    let item=entries.get(id);
    if(!item){
      const box=n('section','tool-section'),head=n('div','tool-section-heading'),title=n('strong'),copy=n('button','tool-action'),content=n(output?'pre':'div',output?'tool-code tool-output':'tool-section-content'),truncated=n('p','tool-truncated');
      copy.type='button';copy.onclick=async()=>{try{await navigator.clipboard.writeText(item.text);copy.textContent=t('copied');}catch{copy.textContent=t('copyFailed');}};
      head.append(title,copy);const follow=n('button','tool-action');follow.type='button';follow.hidden=!output;head.append(follow);box.append(head,content,truncated);
      item={box,title,copy,content,truncated,follow,following:true};entries.set(id,item);
      if(output){content.addEventListener('scroll',()=>{item.following=content.scrollHeight-content.clientHeight-content.scrollTop<24;follow.textContent=t(item.following?'following':'latest');});follow.onclick=()=>{item.following=true;item.signature='';update(current,context);content.scrollTop=content.scrollHeight;};}
    }
    if(item.box.parentElement!==parent)parent.append(item.box);item.box.hidden=false;item.title.textContent=heading;item.copy.textContent=t('copy');
    const text=pretty(value);item.text=text;
    const shown=output?(item.following?text.slice(-LIMIT):(text.length>LIMIT?item.displayed??text.slice(0,LIMIT):text)):text.slice(0,LIMIT);
    const signature=JSON.stringify([structured?value:shown,structured?locale():'',markdown,structured]);
    if(signature!==item.signature){
      item.signature=signature;item.displayed=shown;const top=item.content.scrollTop;
      if(structured)item.content.replaceChildren(renderValue(value));
      else if(markdown)item.content.replaceChildren(renderMarkdown(shown,{downloadFile,acquireFileImage}));
      else item.content.textContent=shown;
      if(output&&item.following)item.content.scrollTop=item.content.scrollHeight;else item.content.scrollTop=top;
    }
    item.truncated.hidden=text.length<=LIMIT;item.truncated.textContent=t('displayTruncated');item.follow.textContent=t(item.following?'following':'latest');
    return item;
  }
  function resultView(result){
    if(result===undefined)return;
    if(Array.isArray(result?.content)){
      const texts=result.content.filter(block=>block.type==='text'&&typeof block.text==='string').map(block=>block.text).join('\n');
      if(texts){let value=texts;try{value=JSON.parse(texts);}catch{/* Plain text remains Markdown. */}section('result',t('result'),value,{structured:typeof value==='object'&&value!==null,markdown:typeof value==='string'});}
      const images=result.content.filter(block=>block.type==='image'&&/^image\/(png|jpeg|webp|gif)$/.test(block.mimeType)&&typeof block.data==='string');
      const item=section('media',t('observations'),'',{});item.box.hidden=!images.length;item.copy.hidden=true;
      if(item.images!==images.map(block=>block.data).join('|')){item.images=images.map(block=>block.data).join('|');item.content.replaceChildren();for(const block of images.slice(0,8)){const img=n('img');img.alt=t('image');img.loading='lazy';img.src=`data:${block.mimeType};base64,${block.data}`;item.content.append(img);}}
      for(const img of item.content.querySelectorAll('img'))img.alt=t('image');
      // Preserve non-text/image blocks as readable fields; raw retains all bytes.
      const other=result.content.filter(block=>!['text','image'].includes(block.type));if(other.length)section('other',t('result'),other,{structured:true});
      const extra=Object.fromEntries(Object.entries(result).filter(([key])=>key!=='content'));if(Object.keys(extra).length)section('result-extra',t('result'),extra,{structured:true});
      if(!result.content.length&&!Object.keys(extra).length)section('result',t('result'),t('emptyOutput'));
      return;
    }
    section('result',t('result'),result,{structured:typeof result==='object'&&result!==null,markdown:typeof result==='string'});
  }
  function browserView(result){
    if(!Array.isArray(result?.content)){resultView(result);return;}
    let page;
    const content=[];
    for(const block of result.content){
      if(block.type==='text'&&typeof block.text==='string'){
        let value;try{value=JSON.parse(block.text);}catch{/* REPL output remains visible. */}
        if(value?.protocol_version===1&&Number.isInteger(value.sequence)&&Array.isArray(value.elements)&&typeof value.title==='string'&&typeof value.url==='string'){
          page=value;continue;
        }
      }
      content.push(block);
    }
    if(!page){resultView(result);return;}
    // AX elements and transport metadata serve the model. The original receipt
    // stays in raw details; the reading view shows the latest page and image.
    section('page',t('page'),{title:page.title,url:page.url},{structured:true});
    if(page.limitations)section('limitations',t('observations'),page.limitations);
    const latestImage=content.findLast(block=>block.type==='image');
    const visible=content.filter(block=>block.type!=='image'||block===latestImage);
    const extra=Object.fromEntries(Object.entries(result).filter(([key])=>!['content','session_id'].includes(key)));
    if(visible.length||Object.keys(extra).length)resultView({...extra,content:visible});
  }
  function taskView(result,args){
    const task=result?.task;
    if(object(task)){
      const live=resolveTask(task.id),title=task.title||live?.title||t('messageTask'),phase=task.phase;
      const item=section('task',t('taskDetails'),{},{}),signature=JSON.stringify([task,locale(),title]);
      item.copy.hidden=true;
      if(item.taskSignature!==signature){item.taskSignature=signature;const head=n('div','tool-task-heading'),name=n('strong','',title);head.append(name);
        if(phase){const key='schedule.state.'+phase,translated=translate(locale(),key);head.append(n('span','tool-task-phase',translated===key?phase:translated));}
        if(onOpenTask&&task.id){const open=n('button','tool-action',t('viewTask'));open.type='button';open.onclick=()=>onOpenTask(resolveTask(task.id)||task);head.append(open);}
        const brand=task.agent_link?.kind||task.executor_icon,executor=task.agent_link?.name||(['pot','codex','antigravity'].includes(brand)?translate(locale(),'agentLinks.source'+(brand==='antigravity'?'Antigravity':brand==='pot'?'Pot':'Codex')):'');
        const details={...(executor?{target:executor}:{}),...(task.model?{model:task.model}:{}),...(typeof task.archived==='boolean'?{archived:task.archived}:{})};
        // Routing IDs, timestamps and the full Task record remain in raw details.
        item.content.replaceChildren(head,renderValue(details));
      }
      if(result.result!==undefined&&result.result!==null)section('task-result',t('latestResult'),result.result,{structured:typeof result.result==='object',markdown:typeof result.result==='string'});
      if(result.error)section('task-error',t('field.error'),result.error,{structured:typeof result.error==='object'});
    }else resultView(result);
    if(!context.compact&&result===undefined&&Object.keys(args).length)section('input',t('arguments'),args,{structured:true});
  }
  function update(tool,next={}){
    current=tool;context=next;const {result,pending,targetLabel='',hasChildren=false,compact=false}=next,args=toolArgs(tool),kind=toolKind(tool);
    element.dataset.kind=kind;for(const item of entries.values())item.box.hidden=true;terminal.hidden=kind!=='terminal'||!!pending;sections.hidden=!!pending;if(pending)return;
    if(kind==='terminal'){
      location.textContent=[targetLabel,tool.workdir||args?.workdir].filter(Boolean).join(' · ')||t('executionContext');shell.textContent=args?.shell||'';bar.hidden=false;
      if(!compact&&tool.name==='exec_command')section('input',t('command'),args?.cmd||'',{parent:terminalSections}).content.classList.add('tool-command');
      else if(!compact){
        const actions=[args?.chars?`${t('sendInput')}\n${args.chars}`:'',args?.interrupt?t('interruptProcess'):'',args?.terminate?t('terminateProcess'):'',args?.close_stdin?t('closeInput'):'',args?.rows||args?.cols?t('resizeTerminal').replace('{rows}',args.rows||'—').replace('{cols}',args.cols||'—'):''].filter(Boolean);
        if(actions.length)section('input',t('sessionActions'),actions.join('\n'),{parent:terminalSections}).content.classList.add('tool-session-input');
      }
      const output=(tool.output||[]).map(chunk=>`${chunk.stream==='stderr'?'[stderr]\n':''}${chunk.text}`).join('\n')||[result?.stdout,result?.stderr?`[stderr]\n${result.stderr}`:''].filter(Boolean).join('\n');
      section('output',t('output'),output||t(activeTool(tool)?'noOutput':'emptyOutput'),{output:true,parent:terminalSections});
      footer.replaceChildren();if(result?.exit_code!=null)footer.append(n('span',result.exit_code===0?'tool-exit-success':'tool-exit-failure',`${t('exitCode')} ${result.exit_code}`));
      if(result?.truncated)footer.append(n('span','tool-truncated',t('sourceTruncated')));footer.hidden=!footer.childNodes.length;
      if(result!==undefined&&(!object(result)||!['stdout','stderr','exit_code','session_id'].some(key=>key in result)))resultView(result);
      return;
    }
    if(!args){if(!compact)section('input',t('preparingArguments'),typeof tool.args==='string'?tool.args:'');resultView(result);return;}
    if(kind==='patch'){
      if(compact){resultView(result);return;}
      const patch=args.patch||'',groups=patchGroups(patch);let remaining=LIMIT;
      if(groups.length){
        const signature=JSON.stringify([patch,locale()]),item=section('patch',t('changes'),patch);item.copy.hidden=false;
        if(item.patchSignature!==signature){item.patchSignature=signature;item.content.replaceChildren();
          for(const file of groups.slice(0,40)){
            const box=n('section','tool-file-diff'),head=n('div','tool-file-heading'),name=n('span','',`${file.path} · ${t(file.operation)}`),counts=n('span','tool-file-counts',`+${file.added} −${file.removed}`),code=n('pre','tool-code tool-diff');
            counts.hidden=file.operation==='deleted'&&!file.lines.length;head.append(name,counts);if(file.destination)head.append(n('span','tool-file-destination','→ '+file.destination));
            for(const line of file.lines){if(remaining<=0)break;const visible=line.slice(0,remaining);remaining-=line.length+1;code.append(n('span',line.startsWith('+')?'tool-diff-add':line.startsWith('-')?'tool-diff-remove':'',visible+'\n'));}
            box.append(head,code);item.content.append(box);if(remaining<=0)break;
          }
          if(groups.length>40)item.content.append(n('p','tool-truncated',t('structureTruncated')));
        }
      }else section('input',t('changes'),patch||args,{structured:!patch});
      resultView(result);return;
    }
    if(kind==='script'||kind==='browser'){
      if(!compact&&!hasChildren&&typeof args.code==='string')section('input',t('code'),args.code).content.classList.add('tool-script-code');
      if(kind==='script'&&typeof result?.output==='string')section('result',t('scriptOutput'),tool.script?.output||result.output||t('emptyOutput'),{output:true});
      else if(kind==='browser')browserView(result);
      else resultView(result);
      const output=(tool.output||[]).map(chunk=>chunk.text).join('\n');if(output)section('output',t('output'),output,{output:true});
      if(!hasChildren&&kind==='script'&&result===undefined&&!args.code)section('input',t('wait'),t('waitingResult'));
      return;
    }
    if(kind==='task'){taskView(result,args);return;}
    if(kind==='resource'){
      const resource=tool.name==='register_resource'?result:result?.presentation?.resource;
      if(object(resource)){
        section('resource',resource.name||args.path||t('resourceDetails'),{name:resource.name,mime_type:resource.mime_type,size:resource.size,files:resource.files?.map(file=>({path:file.path,mime_type:file.mime_type,size:file.size}))},{structured:true});
        section('receipt',t('result'),t(tool.name==='register_resource'?'resourceRegistered':'resourcePresented'));
      }else{if(!compact)section('input',t('resourceDetails'),args,{structured:true});resultView(result);}return;
    }
    if(!Object.hasOwn(toolLabels,tool.name))section('source',t('name'),tool.name);
    if(!compact&&Object.keys(args).length)section('input',t(kind==='schedule'?'scheduleDetails':'arguments'),args,{structured:true});
    resultView(result);
    const output=(tool.output||[]).map(chunk=>chunk.text).join('\n');if(output)section('output',t('output'),output,{output:true});
    if(result===undefined&&!Object.keys(args).length)section('pending',t('result'),t(activeTool(tool)?'waitingResult':'resultUnavailable'));
  }
  return {element,update};
}
