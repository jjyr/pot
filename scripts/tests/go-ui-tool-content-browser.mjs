import {createToolCall} from '../../web/src/components/tool-call.mjs';
import {translate} from '../../web/src/locales/messages.mjs';
const params=new URLSearchParams(location.search),review=params.has('review'),host=document.querySelector('#cases'),output=document.querySelector('#result'),memory=new Map(),cards=[];
let locale=params.get('locale')||'en',openedTask;
document.documentElement.lang=locale;document.documentElement.dataset.theme=params.get('theme')||'light';document.body.classList.toggle('review',review);
const t=key=>translate(locale,'tool.'+key),check=(ok,message)=>{if(!ok)throw Error(message);},frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
const liveTask={id:'task-one',title:'Review mobile layout',phase:'complete'};
function card(id,name,args,result,phase='completed'){
  const value={tool:{id,name,args,result,phase,turn_id:'fixture',started_at_ms:1700000000000,finished_at_ms:phase==='completed'?1700000000200:undefined,output:[]}};
  const view=createToolCall({key:id,memory,locale:()=>locale,machineLabel:()=> 'Development Mac',resolveTask:id=>id===liveTask.id?liveTask:undefined,onOpenTask:task=>openedTask=task});
  host.append(view.element);view.update(value);const entry={view,value,root:view.element};cards.push(entry);return entry;
}
async function expand(entry){entry.root.open=true;await frame();}
const command=card('command','exec_command',{cmd:"rg -n 'createToolCall' web/src",workdir:'/workspace/pot',shell:'/bin/zsh'},{stdout:'web/src/components/tool-call.mjs\n18: export function createToolCall({...}) {\n\nweb/src/ui.js\n27: import {createToolCall} from \'./components/tool-call.mjs\';',exit_code:0});
command.value.tool.target={runtime_id:'dev'};command.value.tool.workdir='/workspace/pot';command.view.update(command.value);
const patch='*** Begin Patch\n*** Update File: web/src/components/tool-call.mjs\n@@\n const args = tool.args || {};\n-section("input", t("arguments"), args);\n+const view = resolveToolView(tool);\n+view.update(tool);\n*** End Patch';
const change=card('patch','apply_patch',{patch},{output:'Updated web/src/components/tool-call.mjs'});
const task=card('task','get_task',{task_id:'task-one'},{task:{id:'task-one',title:'Review mobile layout',phase:'running',model:'test-model',machine_id:'internal-machine-id',run_id:'internal-run-id'},result:'Checked the compact layout. Reviewing output scrolling.'});
const schedule=card('schedule','manage_schedule',{action:'create',spec:{title:'Morning review',kind:'cron',cron:'0 9 * * 1-5',timezone:'Asia/Shanghai',enabled:true}},{id:'plan-one',title:'Morning review',enabled:true});
const resource=card('resource','register_resource',{path:'activity-design.html'},{id:'resource-one',name:'activity-design.html',mime_type:'text/html',size:18000,files:[{path:'activity-design.html',mime_type:'text/html',size:18000}]});
const external=card('external','mcp_lookup',{query:'tool activity',limit:2},{content:[{type:'text',text:JSON.stringify({items:[{title:'Terminal activity',description:'Command and output in one view'},{title:'Task activity',description:'Receipt status and task state are separate'}],total:2})}]});
try{
  if(review){
    const scenario=params.get('scenario')||'terminal';
    for(const entry of scenario==='details'?[task,schedule,resource]:[command,change])await expand(entry);
    window.toolContentFixture={ready:true};
  }else{
    const {toolKind,toolArgs,patchGroups}=await import('../../web/src/state/tool-presentation.mjs');
    check(!command.root.open&&command.root.querySelectorAll('.tool-section').length===0,'collapsed calls defer content rendering');
    command.root.querySelector('summary').click();check(command.root.querySelector('.tool-terminal .tool-command')?.textContent===command.value.tool.args.cmd&&memory.get('command')?.open,'activation renders and saves disclosure before a second action');await frame();
    check(command.root.querySelector('.tool-terminal-location').textContent.includes('Development Mac'),'resolved execution target in terminal');
    check(command.root.querySelector('.tool-terminal-footer').textContent.includes(t('exitCode')+' 0'),'terminal exit receipt');
    check(!command.root.querySelector('.tool-body>.tool-metadata'),'technical metadata is secondary');
    Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async text=>window.toolContentCopied=text}});
    command.root.querySelector('.tool-terminal .tool-section .tool-action').click();await frame();check(window.toolContentCopied===command.value.tool.args.cmd,'copy excludes visual prompt');
    const retained=command.root.querySelector('.tool-output'),commandText=command.root.querySelector('.tool-command').firstChild;
    const selection=getSelection(),range=document.createRange();range.selectNodeContents(commandText);selection.removeAllRanges();selection.addRange(range);
    command.value.tool={...command.value.tool,result:{stdout:'line\n'.repeat(100),exit_code:0}};command.view.update(command.value);await frame();
    check(selection.toString()===command.value.tool.args.cmd,'output refresh retains command selection');selection.removeAllRanges();
    retained.scrollTop=0;retained.dispatchEvent(new Event('scroll'));command.value.tool={...command.value.tool,result:{stdout:'line\n'.repeat(100)+'LATEST',exit_code:0}};command.view.update(command.value);await frame();
    check(command.root.querySelector('.tool-output')===retained&&retained.scrollTop===0,'same output node and paused scroll');
    command.root.querySelector('.tool-output').parentElement.querySelectorAll('.tool-action')[1].click();await frame();check(retained.scrollTop>0,'explicit latest resumes following');
    const full='first\n'+'output '.repeat(5000)+'\nLAST';command.value.tool={...command.value.tool,result:{stdout:full,exit_code:1,truncated:true},phase:'failed'};command.view.update(command.value);await frame();
    check(retained.textContent.endsWith('LAST')&&retained.textContent.length<=24000,'bounded output shows latest retained data');
    const outputSection=retained.closest('.tool-section');outputSection.querySelector('.tool-action').click();await frame();check(window.toolContentCopied===full,'copy includes complete retained output');
    check(command.root.querySelector('.tool-terminal-footer').textContent.includes(t('sourceTruncated')),'source truncation explicit');
    command.root.querySelector('.tool-raw>summary').click();check(memory.get('command')?.raw===true,'raw disclosure saved before virtual-window recycling');await frame();check(command.root.querySelector('.tool-raw pre')&&command.root.querySelector('.tool-raw').textContent.includes('exec_command'),'original JSON in secondary disclosure');
    const raw=command.root.querySelector('.tool-raw');check(raw.open,'raw disclosure open');
    const beforeText=retained.firstChild;locale=locale==='en'?'zh-CN':'en';for(const entry of cards)entry.view.update(entry.value);await frame();
    check(command.root.open&&raw.open&&retained.firstChild===beforeText,'locale keeps disclosures and output text nodes');check(command.root.querySelector('.tool-terminal-footer').textContent.includes(t('sourceTruncated')),'locale refreshes footer');
    locale=params.get('locale')||'en';for(const entry of cards)entry.view.update(entry.value);
    await expand(change);check(change.root.querySelectorAll('.tool-file-diff').length===1&&change.root.querySelector('.tool-file-counts').textContent==='+2 −1','per-file diff counts');
    check(change.root.querySelector('.tool-diff-add').textContent.includes('resolveToolView'),'actual diff content');
    const groups=patchGroups('*** Begin Patch\n*** Update File: old.txt\n*** Move to: new.txt\n@@\n-old\n+new\n*** Delete File: gone.txt\n*** End Patch');check(groups[0].destination==='new.txt'&&groups[1].operation==='deleted','move and delete retain file identity');
    await expand(task);check(task.root.querySelector('.tool-status').textContent===t('completed'),'read receipt is complete');
    check(task.root.querySelector('.tool-task-phase').textContent===translate(locale,'schedule.state.running'),'retained task state stays running despite current directory state');
    check(!task.root.querySelector('.tool-content').textContent.includes('internal-machine-id')&&!task.root.querySelector('.tool-content').textContent.includes('internal-run-id'),'routing identifiers stay in raw details');
    task.root.querySelector('.tool-task-heading button').click();check(openedTask===liveTask,'navigation uses existing live task');
    const taskName=task.root.querySelector('.tool-task-heading strong').firstChild;task.view.update({...task.value,tool:{...task.value.tool}});check(task.root.querySelector('.tool-task-heading strong').firstChild===taskName,'equivalent snapshot preserves task nodes');
    await expand(schedule);check(schedule.root.querySelectorAll('.tool-fields').length>0&&!schedule.root.querySelector('.tool-content pre'),'schedule uses fields rather than JSON');
    check(schedule.root.querySelector('.tool-title').textContent===t('scheduleAction.create'),'schedule action has localized operation title');
    await expand(resource);check(resource.root.querySelector('.tool-content').textContent.includes(t('resourceRegistered'))&&!resource.root.querySelector('iframe'),'registration does not invent a preview');
    const presented=card('presented','present_resource',{resource_id:'resource-one',mode:'preview'},{presentation:{resource:resource.value.tool.result,mode:'preview'}});await expand(presented);check(presented.root.querySelector('.tool-content').textContent.includes(t('resourcePresented'))&&!presented.root.querySelector('iframe'),'presentation receipt does not duplicate the conversation viewer');
    await expand(external);check(external.root.querySelector('.tool-value-list')&&!external.root.querySelector('.tool-content pre'),'MCP JSON text becomes readable records');
    const unsafe=card('unsafe','unknown_tool',{path:'<img src=x onerror="window.injected=true">',custom_field:'verbatim'},{content:[{type:'text',text:'<script>window.injected=true</script> [bad](javascript:alert(1))'}]});await expand(unsafe);
    check(!unsafe.root.querySelector('script')&&!unsafe.root.querySelector('a[href^="javascript:"]')&&!unsafe.root.querySelector('img[src=x]')&&!window.injected,'untrusted args and output remain inert');
    check(unsafe.root.querySelector('.tool-fields').textContent.includes('custom_field'),'unknown field names remain truthful');
    const prototype=card('prototype','constructor',{},false);await expand(prototype);check(prototype.root.querySelector('.tool-title').textContent===t('externalTool'),'extension names never resolve inherited object properties');
    const large=card('large','unknown_tool',{},{items:Array.from({length:1000},()=>({text:'x'.repeat(10000)}))});await expand(large);check(large.root.querySelector('.tool-content').textContent.length<27000&&large.root.querySelector('.tool-truncated'),'bounded structured results');
    const partial=card('partial','unknown_tool','{"query":',undefined,'preparing');await expand(partial);check(partial.root.querySelector('.tool-content').textContent.includes(t('preparingArguments')),'partial arguments identified');
    check(toolArgs(partial.value.tool)===null&&toolKind({name:'cua_repl'})==='browser','partial args and tool classification');
    const browser=card('browser','cua_repl',{code:'await tab.getState();'},{content:[{type:'text',text:'Observed Pot chat'}]});await expand(browser);check(browser.root.querySelector('.tool-script-code').textContent==='await tab.getState();','browser source retained without inferred actions');
    const script=card('script','exec',{code:'await tools.exec_command({cmd:"pwd"});'},{output:'script result',completed:true});script.root.open=true;script.view.update(script.value,{children:[command.value]});await frame();check(!script.root.querySelector('.tool-script-code'),'composition children replace duplicate source');
    const assistantFiles=card('assistant-files','apply_patch',{patch:'*** Begin Patch\n*** Update File: /assistant/AGENTS.md\n@@\n-old rules\n+new rules\n*** End Patch'},{output:'Updated /assistant/AGENTS.md'});await expand(assistantFiles);check(assistantFiles.root.querySelector('.tool-file-heading').textContent.includes('/assistant/AGENTS.md')&&assistantFiles.root.querySelector('.tool-diff-add').textContent.includes('new rules'),'assistant files use the ordinary file diff');
    const pending=card('approval','exec_command',{cmd:'rm sample.txt'},undefined,'preparing');pending.view.update(pending.value,{approval:{id:'review',request:{tool:'exec_command',machine_id:'dev',args:{cmd:'rm sample.txt'}}}});await frame();check(pending.root.open&&!pending.root.querySelector('.tool-approval').hidden&&pending.root.querySelector('.tool-terminal').hidden,'exact approval takes priority over terminal');
    check(host.scrollWidth<=host.clientWidth+1,'expanded content fits viewport');
    output.textContent='PASS: terminal, live output, copying, selection, source truncation, raw details, diff, task status/navigation, schedules, resources, MCP, safe rendering, bounded trees, partial args, composition, assistant files, approvals and locale';
    window.toolContentFixture={ready:true,passed:true};
  }
}catch(error){output.textContent='FAIL: '+error.message;window.toolContentFixture={ready:true,passed:false,error:error.message};throw error;}
