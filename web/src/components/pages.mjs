import {node as n, action, field, section, table, details} from './primitives.mjs';
import {modelDefaults,providerChoices} from '../state/models.mjs';
import {ownedItems,combineTasks,combineUsage} from '../state/aggregate.mjs';
import {createEditorSession, createRequestGate} from '../state/editor.mjs';
import {configurePush,forgetBrowserAccess} from '../adapters/access.mjs';
import {renderMarkdown} from './markdown.mjs';

export function createPages({client, host, approvalHost, locale, notify, changed, titleChanged, openChat}) {
  const gate = createRequestGate(), approvalGate = createRequestGate();
  let route = '', title = '', editor = null, disposed = false, pageVersion = 0, configurationSpace = '';
  let filter = {taskScope:'active', taskQuery:'', taskOffset:0, taskTab:'tasks', usageDays:30, usageScope:'all', usageOffset:0,usageSpace:'all'};
  const cached = new Map(),spaceCache = new Map();
  const tr = (en, zh) => locale() === 'zh-CN' ? zh : en;
  const scope = () => client.scope();
  const request = (path, options = {}) => client.request(path, options);
  const uid = () => crypto.randomUUID();
  const list = value => Array.isArray(value) ? value : value?.items || [];
  async function across(path, token, {spaceID='all', paginate=false,perAssistant=false}={}) {
    const spaces=(client.spaces||[]).filter(space=>spaceID==='all'||space.id===spaceID);
    if(!spaces.length)throw Error(tr('Space is unavailable.','Space 不可用。'));
    const targets=spaces.flatMap(space=>perAssistant?(client.assistants||[]).filter(item=>item.space_id===space.id).map(item=>({space,owner:item.id})):[{space,owner:''}]);
    const results=await Promise.allSettled(targets.map(async({space,owner})=>{
      const key=JSON.stringify([space.id,owner,path]);
      try {
        let value=await request(path,{owner,space:space.id,signal:token.signal});
        if(paginate&&value?.next_offset!=null) {
          let next=value.next_offset,seen=new Set();const items=[...(value.items||[])];
          while(next!=null&&items.length<5000) {if(seen.has(next))throw Error('Invalid pagination cursor');seen.add(next);const url=new URL(path,location.origin);url.searchParams.set('offset',next);const page=await request(url.pathname+url.search,{owner,space:space.id,signal:token.signal});items.push(...(page.items||[]));next=page.next_offset;}
          if(next!=null)throw Error(tr('Too many records. Narrow the filter.','记录过多，请缩小筛选范围。'));value={...value,items,next_offset:null};
        }
        if(gate.current(token))spaceCache.set(key,value);return {space,owner,value,stale:false};
      }catch(reason){if(reason.name==='AbortError')throw reason;const value=spaceCache.get(key);if(value)return {space,owner,value,stale:true,error:reason.message};throw Object.assign(reason,{space});}
    }));
    if(!gate.current(token))return [];
    const failed=results.filter(value=>value.status==='rejected');
    const pages=results.filter(value=>value.status==='fulfilled').map(value=>value.value);
    if(failed.length||pages.some(page=>page.stale)) {const message=n('p','inline-error',tr('Some Spaces are unavailable; totals may be incomplete and cached rows are marked stale.','部分 Space 不可用，用量可能不完整，缓存项目标记为过期。'));host.querySelector('.page-content')?.prepend(message);}
    if(!pages.length&&targets.length)throw failed[0]?.reason||Error('No Space data');return pages;
  }
  function heading(value) {title=value; titleChanged();}
  function errorView(reason, target = host) {
    const box=n('div','inline-error');box.setAttribute('role','alert');box.append(n('span','',reason.message || String(reason)),action(tr('Retry','重试'),()=>show(route)));target.append(box);
  }
  function body() {const value=n('div','page-content');host.replaceChildren(value);return value;}
  function toolbar(...children) {const result=n('div','page-toolbar');result.append(...children);return result;}
  async function operation(button, work, after = () => show(route)) {
    const version=pageVersion;button.disabled=true;
    try {await work(); if(!disposed&&version===pageVersion) await after();}
    catch(reason) {if(!disposed&&version===pageVersion) errorView(reason);}
    finally {button.disabled=false;}
  }
  async function load(work) {
    const token=gate.next(), root=body();root.append(n('p','hint',tr('Loading…','正在加载…')));
    try {const value=await work(token);if(gate.current(token)&&!disposed) {root.replaceChildren();return {root,value,token};}}
    catch(reason) {if(gate.current(token)&&!disposed) {root.replaceChildren();errorView(reason,root);}}
  }
  function empty(root, text) {root.append(n('p','page-empty',text));}
  async function confirmDiscard() {
    return new Promise(resolve=>{
      const dialog=n('dialog','preferences'), title=n('h2','',tr('Discard changes?','放弃更改？'));
      const actions=toolbar(action(tr('Keep editing','继续编辑'),()=>{dialog.close();resolve(false);}),action(tr('Discard changes','放弃更改'),()=>{dialog.close();resolve(true);},{danger:true}));
      dialog.append(title,n('p','hint',tr('Your unsaved changes will be lost.','未保存的更改将丢失。')),actions);document.body.append(dialog);
      dialog.addEventListener('cancel',event=>{event.preventDefault();dialog.close();resolve(false);},{once:true});dialog.addEventListener('close',()=>dialog.remove(),{once:true});dialog.showModal();
    });
  }
  async function leave() {
    if(!editor){gate.close();return true;}
    if(editor.session.pending) {notify(tr('Please wait for the save result.','请等待保存结果。'));return false;}
    if(editor.session.dirty&&!await confirmDiscard())return false;
    editor=null;gate.close();host.replaceChildren();return true;
  }
  function renderEditor(label, initial, definitions, save, {returnChat=false,capturedScope}={}) {
    gate.close();
    const captured=capturedScope||scope(), caller=route || 'settings';
    const session=createEditorSession({...captured,route:caller,boot:client.runtime.boot_id},initial);
    const root=body(), form=n('form','editor-form'), fields=n('div','editor-fields'), footer=n('div','editor-actions'), status=n('p','inline-error');status.setAttribute('role','alert');
    const controls=new Map(),lifecycle={};
    for(const definition of definitions) {
      if(definition.section) {fields.append(section(definition.section));continue;}
      const {name,label,parse,presets,...options}=definition;
      const item=field(label,initial[name],options);controls.set(name,item.input);
      if(presets){const choices=toolbar(...presets.map(value=>action(value>=1000000?`${value/1000000}M`:`${value/1000}K`,()=>{item.input.value=value;item.input.dispatchEvent(new Event('input',{bubbles:true}));})));choices.classList.add('token-presets');item.row.lastChild.append(choices);}
      item.input.addEventListener('input',()=>session.set(name,parse?parse(item.input.value):options.kind==='checkbox'?item.input.checked:item.input.value));fields.append(item.row);
    }
    const cancel=action(tr('Cancel','取消'),()=>back());const submit=action(tr('Save','保存'),()=>form.requestSubmit(),{primary:true});
    footer.append(cancel,submit);form.append(fields,status,footer);root.append(form);
    editor={session,caller,returnChat,id:uid()};
    history.pushState({...history.state,potEditor:editor.id},'',location.href);heading(label);
    form.onsubmit=async event=>{
      event.preventDefault();if(!form.reportValidity())return;
      const attempt=session.begin();if(!attempt)return;
      status.textContent='';for(const control of controls.values())control.disabled=true;cancel.disabled=submit.disabled=true;
      try {
        if(client.runtime.boot_id!==session.context.boot)throw Error(tr('Runtime identity changed. Reopen this editor.','运行时身份已变化，请重新打开编辑器。'));
        const saved=await save(attempt.values,{...captured,request_id:attempt.request_id},session);
        session.settle(true);changed(saved);await back();
      } catch(reason) {session.settle(false);status.textContent=reason.message||String(reason);}
      finally {for(const control of controls.values())control.disabled=false;cancel.disabled=submit.disabled=false;lifecycle.settled?.();}
    };
    return {session,root,form,controls,lifecycle};
  }
  async function back() {const current=editor;if(!await leave())return;const state={...history.state};delete state.potEditor;history.replaceState(state,'',location.href);if(current?.returnChat)openChat();else show(current?.caller || route || 'settings');}
  async function assistant(definition) {
    if(!await leave())return;
    const token=gate.next(),captured=scope(),loading=body();loading.append(n('p','hint',tr('Loading…','正在加载…')));
    try{
      const [providers,thread,rules]=await Promise.all([request('/api/v1/providers',captured),definition?request('/api/v1/agent',captured):null,definition?request('/api/v1/instructions',captured):null]);
      if(!gate.current(token)||disposed)return;
      let profiles=providers.profiles||[],revision=definition?.revision,generation=thread?.generation||0,configurationRevision=thread?.revision||0,rulesRevision=rules?.revision||'';
      const selection=thread?.selection||{},profileID=selection.profile_id||thread?.config?.source_profile_id||providers.default_provider_id||'',profile=profiles.find(value=>value.id===profileID);
      const initial={space_id:captured.space,name:definition?.name||'',profile_id:profileID,model:selection.model||thread?.config?.model||profile?.model||'',model_selection:'',context_window:selection.context_window??null,max_output_tokens:selection.max_output_tokens??null,reasoning_effort:selection.reasoning_effort||'',text:rules?.text||''};
      const selectValue=values=>{const profile=profiles.find(value=>value.id===values.profile_id);return profile?{profile_id:profile.id,profile_revision:profile.revision,model:values.model,context_window:values.context_window||null,max_output_tokens:values.max_output_tokens||null,reasoning_effort:values.reasoning_effort.trim()||null}:null;};
      let initialSelection=JSON.stringify({...selectValue(initial),profile_revision:selection.profile_revision||profile?.revision});
      const form=renderEditor(definition?tr('Assistant settings','助手设置'):tr('New assistant','新建助手'),initial,[
        ...(!definition&&client.desktop?[{name:'space_id',label:tr('Location','位置'),choices:(client.spaces||[]).map(space=>[space.id,space.name])}]:[]),
        {name:'name',label:tr('Name','名称'),required:true},
        {name:'profile_id',label:tr('Provider','提供商'),choices:[['',tr('Select a Provider','选择提供商')],...profiles.map(value=>[value.id,value.name])]},
        {name:'model_selection',label:tr('Model','模型'),choices:[['',tr('Enter model ID','输入模型 ID')]]},
        {name:'model',label:tr('Model ID','模型 ID')},
        {name:'context_window',label:tr('Context window','上下文窗口'),kind:'number',parse:value=>value?Number(value):null,min:1,presets:[32000,64000,128000,200000,1000000]},
        {name:'max_output_tokens',label:tr('Maximum output tokens','最大输出 token'),kind:'number',parse:value=>value?Number(value):null,min:1,presets:[4000,8000,16000,32000]},
        {name:'reasoning_effort',label:tr('Reasoning effort','推理强度')},
        {name:'text',label:tr('Instructions','指令'),kind:'textarea',help:tr('Rules are saved for this assistant.','指令仅保存到此助手。')},
      ],async(values,context,session)=>{
        const options={owner:captured.owner,space:captured.space,method:'POST'},selection=selectValue(values);
        if(!definition){const saved=await request('/api/v1/assistants',{...options,space:values.space_id,owner:'',body:{request_id:context.request_id,name:values.name,text:values.text,...(selection?{selection}:{})}});return {assistant:{...saved,space_id:values.space_id}};}
        if(values.name!==initial.name){const saved=await request(`/api/v1/assistants/${encodeURIComponent(definition.id)}`,{...options,body:{name:values.name,revision}});revision=saved.revision;initial.name=values.name;session.acknowledge({name:values.name});}
        const signature=JSON.stringify(selection);
        if(selection&&signature!==initialSelection){const saved=await request('/api/v1/agent',{...options,body:{request_id:context.request_id,expected_generation:generation,expected_revision:configurationRevision,selection}});generation=saved.generation;configurationRevision=saved.revision;initialSelection=signature;session.acknowledge(Object.fromEntries(['profile_id','model','model_selection','context_window','max_output_tokens','reasoning_effort'].map(key=>[key,values[key]])));}
        if(values.text!==initial.text){const saved=await request('/api/v1/instructions',{...options,body:{text:values.text,revision:rulesRevision}});rulesRevision=saved.revision;initial.text=values.text;session.acknowledge({text:values.text});}
      },{returnChat:true});
      const controls=form.controls,session=form.session;
      const set=(name,value)=>{controls.get(name).value=value??'';session.set(name,value);};
      const defaults=()=>{const profile=profiles.find(value=>value.id===session.values.profile_id),catalog=profile?.model_settings?.catalog_provider||'custom',limits=modelDefaults(providers,catalog,session.values.model);const same=profile?.model===session.values.model;controls.get('context_window').placeholder=String(same?(profile?.model_settings?.context_window||limits.context):limits.context);controls.get('max_output_tokens').placeholder=String(same?(profile?.model_settings?.max_output_tokens||limits.output):limits.output);controls.get('reasoning_effort').placeholder=profile?.params?.reasoning_effort||tr('Provider default','提供商默认值');controls.get('model').required=!!profile;};
      const updateModels=()=>{const profile=profiles.find(value=>value.id===session.values.profile_id),catalog=(providers.directory||[]).find(value=>value.id===profile?.model_settings?.catalog_provider);const options=new Map();if(profile?.model)options.set(profile.model,profile.model);for(const model of catalog?.models||[])options.set(model.id,model.name||model.id);for(const model of profile?.models||[])options.set(typeof model==='string'?model:model.id,typeof model==='string'?model:model.name||model.id);const input=controls.get('model_selection');input.replaceChildren();for(const [value,label]of [['',tr('Enter model ID','输入模型 ID')],...options]){const option=n('option','',label);option.value=value;input.append(option);}set('model_selection',options.has(session.values.model)?session.values.model:'');session.acknowledge({model_selection:session.values.model_selection});controls.get('model').closest('.form-row').hidden=!!input.value;defaults();};
      controls.get('profile_id').addEventListener('change',()=>{const selected=profiles.find(value=>value.id===session.values.profile_id);set('model',selected?.model||'');updateModels();});
      controls.get('model_selection').addEventListener('change',()=>{if(controls.get('model_selection').value)set('model',controls.get('model_selection').value);controls.get('model').closest('.form-row').hidden=!!controls.get('model_selection').value;defaults();});controls.get('model').addEventListener('input',defaults);updateModels();
      if(thread?.pending)form.root.prepend(n('p','hint',tr('Saved settings will apply to the next new turn. The current turn keeps its original configuration.','已保存的设置将在下一次新回合生效，当前回合保持原配置。')));
      if(!definition&&controls.has('space_id'))controls.get('space_id').addEventListener('change',async()=>{const next=controls.get('space_id').value;controls.get('profile_id').disabled=true;try{const snapshot=await request('/api/v1/providers',{space:next,owner:''});if(editor?.session!==session||controls.get('space_id').value!==next)return;profiles=snapshot.profiles||[];Object.assign(providers,snapshot);const input=controls.get('profile_id');input.replaceChildren();for(const profile of profiles){const option=n('option','',profile.name);option.value=profile.id;input.append(option);}set('profile_id',snapshot.default_provider_id||'');set('model',profiles.find(value=>value.id===session.values.profile_id)?.model||'');updateModels();}catch(reason){if(editor?.session===session)errorView(reason,form.root);}finally{if(editor?.session===session)controls.get('profile_id').disabled=false;}});
    }catch(reason){if(gate.current(token))errorView(reason);}
  }
  async function settings(root, token) {
    const preferences=section(tr('General','通用'));
    for(const [label,value,choices,key] of [
      [tr('Language','语言'),locale(),[['en','English'],['zh-CN','简体中文']],'pot-go-preview-locale'],
      [tr('Appearance','外观'),document.documentElement.dataset.theme||'system',[['system',tr('System','跟随系统')],['light',tr('Light','浅色')],['dark',tr('Dark','深色')]],'pot-go-preview-theme']]) {
      const item=field(label,value,{choices});item.input.onchange=()=>{try{localStorage.setItem(key,item.input.value);}catch{}window.dispatchEvent(new CustomEvent('pot:preference',{detail:{key,value:item.input.value}}));};preferences.append(item.row);
    }root.append(preferences);
    if(!client.desktop&&client.access){const access=section(tr('Browser access','浏览器访问'));if(client.access.push_available){const enable=action(tr('Enable notifications','启用通知'),()=>operation(enable,()=>configurePush(client.access,{locale:locale()}),()=>{access.append(n('p','success-text',tr('Notifications enabled','通知已启用')));}));access.append(enable);}access.append(action(tr('Disconnect this browser','断开此浏览器'),()=>forgetBrowserAccess()));root.append(access);}
    const locations=client.spaces||[];if(!configurationSpace)configurationSpace=scope().space;
    const settingsScope={space:configurationSpace,owner:''};
    if(client.desktop&&locations.length>1){const location=field(tr('Configuration location','配置位置'),configurationSpace,{choices:locations.map(space=>[space.id,space.name])});location.input.onchange=()=>{configurationSpace=location.input.value;show('settings');};root.append(location.row);}
    const providers=await request('/api/v1/providers',{...settingsScope,signal:token.signal});if(!gate.current(token))return;
    const ai=section(tr('AI connections and models','AI 连接与模型'));ai.append(toolbar(action(tr('Add connection','添加连接'),()=>providerEditor(null,providers,settingsScope))));
    if(!(providers.profiles||[]).length)ai.append(n('p','hint',tr('Connect an AI provider, then create an assistant.','先连接 AI 提供商，再创建助手。')));
    for(const profile of providers.profiles||[]){const row=n('div','list-row'),info=n('div','row-info');info.append(n('strong','',profile.name),n('span','hint',profile.model));
      const edit=action(tr('Edit','编辑'),()=>providerEditor(profile,providers,settingsScope));
      const connection=(providers.connections||[]).find(value=>value.profile_id===profile.id);
      const check=action(tr('Check','检查'),()=>operation(check,()=>request('/api/v1/providers/check',{...settingsScope,method:'POST',body:{profile_id:profile.id,endpoint:connection?.endpoint||'',protocol:connection?.protocol||'openai_responses',api_key:''}}),async()=>{row.append(n('span','success-text',tr('Connection checked','连接已检查')));}));
      const choose=action(providers.default_provider_id===profile.id?tr('Default','默认'):tr('Set default','设为默认'),()=>operation(choose,()=>request('/api/v1/providers/default',{...settingsScope,method:'POST',body:{provider_id:profile.id}})),{disabled:providers.default_provider_id===profile.id});
      row.append(info,toolbar(edit,check,choose));ai.append(row);
    }root.append(ai);
    const machines=section(tr('Spaces and connections','Space 与连接'));
    const spaces=await request('/api/v1/spaces',{signal:token.signal,owner:'',space:''});if(!gate.current(token))return;
    for(const space of spaces.items||[]) {const row=n('div','list-row');row.append(n('span','',space.name),n('span','hint',space.connected?tr('Connected','已连接'):tr('Offline','离线')));if(client.desktop&&space.kind==='remote') {for(const name of ['connect','disconnect','remove']) {const button=action(tr({connect:'Connect',disconnect:'Disconnect',remove:'Remove'}[name],{connect:'连接',disconnect:'断开',remove:'移除'}[name]),()=>operation(button,()=>request('/api/v1/spaces',{method:'POST',owner:'',space:'',body:{action:name,space_id:space.id}})));row.append(button);}}machines.append(row);}
    if(client.desktop)machines.append(action(tr('Connect Space','连接 Space'),()=>spaceEditor()));root.append(machines);
    const about=section(tr('About','关于'));about.append(n('p','','Pot'),n('p','hint',tr('Go + Wails v3 · System WebView','Go + Wails v3 · 系统 WebView')),details(tr('Open source licenses','开源许可'),tr('IBM Plex Sans is distributed under the SIL Open Font License.','IBM Plex Sans 使用 SIL 开放字体许可。')));const license=n('a','','IBM Plex Sans — OFL');license.href=new URL('../assets/IBMPlexSans-OFL.txt',import.meta.url);license.target='_blank';license.rel='noopener';about.append(license);root.append(about);
  }
  async function providerEditor(profile, snapshot, settingsScope) {
    if(!await leave())return;
    const connection=(snapshot.connections||[]).find(c=>c.profile_id===profile?.id);
    const agent=profile?.params?.agent||{};
    const selectedProvider=profile?.model_settings?.catalog_provider||(profile?'custom':(snapshot.directory||[]).find(value=>value.common)?.id||'custom');
    const selectedEntry=(snapshot.directory||[]).find(value=>value.id===selectedProvider);
    const inherited=modelDefaults(snapshot,selectedProvider,profile?.model||'');
    const initial={provider_search:'',provider_kind:selectedProvider,model_selection:(selectedEntry?.models||[]).some(value=>value.id===profile?.model)?profile.model:'',context_window:profile?(profile.model_settings?profile.model_settings.context_window??null:agent.context_window||32768):null,max_input_tokens:inherited.input||'',compact_at:agent.compact_at||'',context_reserve:agent.context_reserve??4096,auto_compact:agent.auto_compact??true,use_provider_usage:agent.use_provider_usage??true,request_timeout_ms:agent.request_timeout_ms||180000,system_prompt:agent.system_prompt||'',builtin_tools:(agent.builtin_tools||['exec_command','write_stdin','apply_patch','exec','wait']).join(', '),allowed_tools:agent.allowed_tools?.join(', ')||'',...Object.fromEntries(Object.entries(agent.plugins||{'computer-use':{enabled:true}}).map(([name,value])=>['plugin:'+name,value.enabled])),name:profile?.name||selectedEntry?.name||'',model:profile?.model||'',endpoint:connection?.endpoint||selectedEntry?.endpoint||'',protocol:connection?.protocol||selectedEntry?.protocol||'openai_responses',api_key:'',max_output_tokens:profile?(profile.model_settings?profile.model_settings.max_output_tokens??null:profile.params?.max_output_tokens||4096):null,reasoning_effort:profile?.params?.reasoning_effort||''};
    const form=renderEditor(profile?tr('Edit AI connection','编辑 AI 连接'):tr('Add AI connection','添加 AI 连接'),initial,[
      {name:'provider_search',label:tr('Find a Provider','搜索提供商'),kind:'search'},
      {name:'provider_kind',label:tr('Provider','提供商'),choices:providerChoices(snapshot,{selected:selectedProvider}).map(([value,label])=>[value,value==='__more'?tr('More providers…','更多提供商…'):value==='custom'?tr('Custom connection','自定义连接'):label])},
      {name:'name',label:tr('Name','名称'),required:true},{name:'protocol',label:tr('Protocol','协议'),choices:[['openai_responses','OpenAI Responses'],['openai_chat','OpenAI Chat Completions'],['anthropic_messages','Anthropic Messages'],['gemini','Google Gemini']]},
      {name:'endpoint',label:tr('Endpoint','端点'),kind:'url',required:true},{name:'api_key',label:tr('API key','API 密钥'),kind:'password',required:!profile,help:profile?tr('Leave blank to keep the saved key.','留空保留现有密钥。'):''},
      {name:'model_selection',label:tr('Model','模型'),choices:[['',tr('Enter model ID','输入模型 ID')],...(selectedEntry?.models||[]).map(value=>[value.id,value.name||value.id])]},
      {name:'model',label:tr('Model ID','模型 ID'),required:true},
      {name:'max_output_tokens',label:tr('Maximum output tokens','最大输出 token'),kind:'number',parse:value=>value?Number(value):null,min:1,placeholder:String(inherited.output),presets:[4000,8000,16000,32000]},
      {name:'reasoning_effort',label:tr('Reasoning effort','推理强度')},
      {section:tr('Context and execution','上下文与执行')},
      {name:'context_window',label:tr('Context window','上下文窗口'),kind:'number',parse:value=>value?Number(value):null,min:1,placeholder:String(inherited.context),presets:[32000,64000,128000,200000,1000000]},
      {name:'max_input_tokens',label:tr('Maximum input tokens','最大输入 token'),kind:'number',parse:value=>value?Number(value):null},
      {name:'compact_at',label:tr('Compact at tokens','压缩阈值 token'),kind:'number',parse:value=>value?Number(value):null},
      {name:'context_reserve',label:tr('Context reserve','预留上下文'),kind:'number',parse:Number},
      {name:'auto_compact',label:tr('Automatic compaction','自动压缩'),kind:'checkbox'},
      {name:'use_provider_usage',label:tr('Use provider token counts','使用提供商 token 计数'),kind:'checkbox'},
      {name:'request_timeout_ms',label:tr('Request timeout (ms)','请求超时（毫秒）'),kind:'number',parse:Number,required:true},
      {name:'system_prompt',label:tr('System instructions','系统指令'),kind:'textarea'},
      {name:'builtin_tools',label:tr('Built-in tools','内置工具'),help:tr('Separate tool names with commas.','工具名称以逗号分隔。')},
      {name:'allowed_tools',label:tr('Allowed tools','允许的工具'),help:tr('Leave blank to use the configured defaults.','留空使用默认配置。')},
      ...Object.keys(agent.plugins||{'computer-use':{enabled:true}}).map(name=>({name:'plugin:'+name,label:`${tr('Plugin','插件')} · ${name}`,kind:'checkbox'})),
    ],(values,context)=>{
      const {provider_search,provider_kind,model_selection,context_window,max_input_tokens,compact_at,context_reserve,auto_compact,use_provider_usage,request_timeout_ms,system_prompt,builtin_tools,allowed_tools,...connectionValues}=values;
      const plugins=structuredClone(agent.plugins||{'computer-use':{enabled:true}});
      for(const key of Object.keys(connectionValues))if(key.startsWith('plugin:')){plugins[key.slice(7)]={...plugins[key.slice(7)],enabled:connectionValues[key]};delete connectionValues[key];}
      return request('/api/v1/providers',{...context,method:'POST',body:{request_id:context.request_id,profile_id:profile?.id||'',expected_revision:profile?.revision||0,...connectionValues,reasoning_effort:values.reasoning_effort.trim()||null,max_output_tokens:values.max_output_tokens||modelDefaults(snapshot,provider_kind,values.model).output,model_settings:{catalog_provider:provider_kind==='custom'?null:provider_kind,context_window:context_window||null,max_output_tokens:values.max_output_tokens||null},
        agent:{...agent,plugins,context_window:context_window||modelDefaults(snapshot,provider_kind,values.model).context,max_input_tokens:max_input_tokens||null,compact_at:compact_at||null,context_reserve,auto_compact,use_provider_usage,request_timeout_ms,system_prompt:system_prompt||null,builtin_tools:builtin_tools.split(',').map(value=>value.trim()).filter(Boolean),allowed_tools:allowed_tools.trim()?allowed_tools.split(',').map(value=>value.trim()).filter(Boolean):null}}});
    },{capturedScope:settingsScope});
    const controls=form.controls,session=form.session;let allProviders=false,providerID=selectedProvider,endpointEdited=false;
    const set=(name,value)=>{const input=controls.get(name);if(input.type==='checkbox')input.checked=value;else input.value=value;session.set(name,value);};
    const showModel=()=>{controls.get('model').closest('.form-row').hidden=!!controls.get('model_selection').value;};
    const inherit=()=>{const limits=modelDefaults(snapshot,providerID,session.values.model);controls.get('context_window').placeholder=String(limits.context);controls.get('max_output_tokens').placeholder=String(limits.output);set('max_input_tokens',limits.input||'');controls.get('max_input_tokens').disabled=true;};
    const models=(entries,selected='')=>{const input=controls.get('model_selection');input.replaceChildren();for(const [value,label]of [['',tr('Enter model ID','输入模型 ID')],...entries.map(model=>[typeof model==='string'?model:model.id,typeof model==='string'?model:model.name||model.id])]){const option=n('option','',label);option.value=value;input.append(option);}set('model_selection',entries.some(model=>(typeof model==='string'?model:model.id)===selected)?selected:'');session.acknowledge({model_selection:session.values.model_selection});showModel();};
    const providerLogo=n('img','provider-logo');providerLogo.alt='';controls.get('provider_kind').closest('.form-row').lastChild.prepend(providerLogo);
    const updateLogo=()=>{const logo=(snapshot.directory||[]).find(value=>value.id===providerID)?.logo;if(typeof logo==='string'&&logo.startsWith('<svg')){providerLogo.src=`data:image/svg+xml;charset=utf-8,${encodeURIComponent(logo)}`;providerLogo.hidden=false;}else providerLogo.hidden=true;};
    controls.get('provider_search').addEventListener('input',()=>{const query=controls.get('provider_search').value.toLowerCase().trim();session.acknowledge({provider_search:session.values.provider_search});const input=controls.get('provider_kind');input.replaceChildren();let choices=query?(snapshot.directory||[]).filter(value=>`${value.id} ${value.name}`.toLowerCase().includes(query)).map(value=>[value.id,value.name]):providerChoices(snapshot,{all:allProviders,selected:providerID});if(!choices.some(([value])=>value===providerID)){const selected=(snapshot.directory||[]).find(value=>value.id===providerID);choices.unshift([providerID,selected?.name||tr('Custom connection','自定义连接')]);}for(const [value,label]of choices){const option=n('option','',value==='__more'?tr('More providers…','更多提供商…'):value==='custom'?tr('Custom connection','自定义连接'):label);option.value=value;input.append(option);}set('provider_kind',providerID);});
    controls.get('provider_kind').addEventListener('change',()=>{const input=controls.get('provider_kind');if(input.value==='__more'){allProviders=true;input.replaceChildren();for(const [value,label]of providerChoices(snapshot,{all:true,selected:providerID})){const option=n('option','',value==='custom'?tr('Custom connection','自定义连接'):label);option.value=value;input.append(option);}set('provider_kind',providerID);return;}providerID=input.value;endpointEdited=false;const provider=(snapshot.directory||[]).find(value=>value.id===providerID);if(provider){set('endpoint',provider.endpoint);set('protocol',provider.protocol);if(!profile)set('name',provider.name);}models(provider?.models||[],session.values.model);inherit();updateLogo();});
    controls.get('model_selection').addEventListener('change',()=>{if(controls.get('model_selection').value){set('model',controls.get('model_selection').value);const model=(snapshot.directory||[]).find(provider=>provider.id===providerID)?.models.find(model=>model.id===session.values.model);if(model&&!endpointEdited){if(model.endpoint)set('endpoint',model.endpoint);if(model.protocol)set('protocol',model.protocol);}}showModel();inherit();});
    controls.get('endpoint').addEventListener('input',()=>{endpointEdited=true;});form.lifecycle.settled=inherit;
    controls.get('model').addEventListener('input',inherit);showModel();inherit();updateLogo();
    const check=action(tr('Check available models','检查可用模型'),async()=>{const captured=structuredClone(session.values);check.disabled=true;try{const value=await request('/api/v1/providers/check',{...settingsScope,method:'POST',body:{endpoint:captured.endpoint,protocol:captured.protocol,api_key:captured.api_key,profile_id:profile?.id||''}});if(editor?.session!==session||['endpoint','protocol','api_key'].some(name=>session.values[name]!==captured[name]))return;models(value.models||[],session.values.model);const status=n('p','success-text',tr('Model list received','已获取模型列表'));form.root.append(status);}catch(reason){if(editor?.session===session)errorView(reason,form.root);}finally{check.disabled=false;}});
    form.form.querySelector('.editor-actions').prepend(check);
  }
  function spaceEditor() {
    renderEditor(tr('Connect Space','连接 Space'),{name:'',endpoint:'',token:'',ca_pem:''},[
      {name:'name',label:tr('Name','名称'),required:true},{name:'endpoint',label:tr('Endpoint','端点'),kind:'url',required:true},{name:'token',label:tr('Access token','访问令牌'),kind:'password',required:true},{name:'ca_pem',label:tr('Trusted Control CA (optional)','受信任的 Control CA（可选）'),kind:'textarea'},
    ],(values,context)=>request('/api/v1/spaces',{method:'POST',owner:'',space:'',body:{action:'connect',request_id:context.request_id,...values}}));
  }
  async function tasks(root,token) {
    const tabs=toolbar(...[['tasks',tr('Tasks','任务')],['schedules',tr('Schedules','计划')]].map(([name,label])=>action(label,()=>{filter.taskTab=name;show('tasks');},{primary:filter.taskTab===name})));root.append(tabs);
    if(filter.taskTab==='schedules')return schedules(root,token);
    const search=field(tr('Search','搜索'),filter.taskQuery,{kind:'search'});search.input.onchange=()=>{filter.taskQuery=search.input.value;filter.taskOffset=0;show('tasks');};
    root.append(toolbar(...['active','attention','archived'].map(value=>action(tr({active:'Active',attention:'Attention',archived:'Archived'}[value],{active:'进行中',attention:'需关注',archived:'已归档'}[value]),()=>{filter.taskScope=value;filter.taskOffset=0;show('tasks');},{primary:filter.taskScope===value}))),search.row);
    const pages=await across(`/api/v1/tasks/query?${new URLSearchParams({scope:filter.taskScope,q:filter.taskQuery,offset:0,limit:50})}`,token,{paginate:true,perAssistant:true});if(!gate.current(token))return;const result=combineTasks(pages,{offset:filter.taskOffset,limit:20});
    if(!result.items?.length)empty(root,tr('No tasks','暂无任务'));
    for(const task of result.items||[]){const row=n('div','list-row'),info=n('div','row-info');info.append(n('strong','',task.title),n('span','hint',`${task.phase} · ${task.assistant_id} · ${task._space_name||''}${task._stale?' · '+tr('Stale','已过期'):''} · ${task.machine_id||''}`));row.append(info,action(tr('Open','打开'),()=>taskDetail(task),{disabled:task._stale}));root.append(row);}
    if(result.next_offset!=null)root.append(action(tr('Next page','下一页'),()=>{filter.taskOffset=result.next_offset;show('tasks');}));
    if(filter.taskOffset)root.append(action(tr('First page','首页'),()=>{filter.taskOffset=0;show('tasks');}));
  }
  async function taskDetail(task) {
    const result=await load(async token=>request(`/api/v1/tasks/${encodeURIComponent(task.id)}`,{signal:token.signal,owner:task.assistant_id,space:task._space_id}));if(!result)return;
    const {root}=result, record=result.value;heading(task.title);
    const fileScope={owner:task.assistant_id,space:task._space_id};
    const downloadFile=async({task:id,path})=>{try{await client.downloadFile(id,path,fileScope);}catch(reason){if(gate.current(result.token))errorView(reason,root);}};
    root.append(toolbar(action(tr('Back to Tasks','返回任务'),()=>show('tasks'))),n('p','hint',`${record.info?.phase||task.phase} · ${task.machine_id||''}`));
    if(record.error)root.append(n('p','inline-error',record.error));
    if(record.request?.text)root.append(section(tr('Goal','目标')),renderMarkdown(record.request.text,{downloadFile}));
    if(record.result)root.append(section(tr('Result','结果')),renderMarkdown(typeof record.result==='string'?record.result:JSON.stringify(record.result),{downloadFile}));
    const inputs=await request(`/api/v1/tasks/${encodeURIComponent(task.id)}/messages`,{owner:task.assistant_id,space:task._space_id});
    if(!gate.current(result.token))return;
    for(const input of list(inputs))root.append(n('p','task-input',input.text));
    const input=field(tr('Message','消息'),'',{kind:'textarea'});root.append(input.row);
    const send=action(tr('Send guidance','发送指导'),()=>operation(send,()=>request(`/api/v1/tasks/${encodeURIComponent(task.id)}/messages`,{owner:task.assistant_id,space:task._space_id,method:'POST',body:{request_id:uid(),text:input.input.value}}),()=>taskDetail(task)),{primary:true});
    const actions=toolbar(send);for(const name of ['interrupt','cancel']) {const button=action(tr(name==='cancel'?'Cancel task':'Interrupt',name==='cancel'?'取消任务':'中断'),()=>operation(button,()=>request(`/api/v1/tasks/${encodeURIComponent(task.id)}/${name}`,{method:'POST',owner:task.assistant_id,space:task._space_id,body:{request_id:uid()}}),()=>taskDetail(task)),{danger:true});actions.append(button);}root.append(actions);
    if(record.files?.length)for(const file of record.files){const link=n('a','',file.name||file.path);link.href='#';link.onclick=event=>{event.preventDefault();downloadFile({task:task.id,path:file.path});};root.append(link);}
  }
  async function schedules(root, token) {
    root.append(toolbar(action(tr('New schedule','新建计划'),()=>scheduleEditor(null),{primary:true})));
    const pages=await across('/api/v1/schedules?limit=50',token,{paginate:true,perAssistant:true});if(!gate.current(token))return;const page={items:pages.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner))};
    if(!page.items?.length)empty(root,tr('No schedules','暂无计划'));
    for(const item of page.items||[]) {const row=n('div','list-row'),info=n('div','row-info');info.append(n('strong','',item.spec.title),n('span','hint',`${item._stale?tr('Stale','已过期'):item.spec.enabled?tr('Enabled','已启用'):tr('Paused','已暂停')} · ${item.next_label||''}`));row.append(info);
      row.append(action(tr('Edit','编辑'),()=>scheduleEditor(item),{disabled:item._stale}));
      for(const name of [item.spec.enabled?'pause':'resume','run','delete']){const button=action(tr({pause:'Pause',resume:'Resume',run:'Run now',delete:'Delete'}[name],{pause:'暂停',resume:'恢复',run:'立即运行',delete:'删除'}[name]),()=>operation(button,()=>request('/api/v1/schedules',{method:'POST',space:item._space_id,owner:item._assistant_id,body:{request_id:uid(),action:name,schedule_id:item.id}})),{danger:name==='delete',disabled:item._stale});row.append(button);}
      const history=action(tr('History','历史'),()=>scheduleHistory(item));row.append(history);root.append(row);
    }
  }
  async function scheduleHistory(item) {
    const result=await load(token=>request(`/api/v1/schedules?selected=${encodeURIComponent(item.id)}`,{signal:token.signal,space:item._space_id,owner:item._assistant_id}));if(!result)return;
    heading(item.spec.title);result.root.append(toolbar(action(tr('Back to Schedules','返回计划'),()=>show('tasks'))),table([tr('Scheduled','计划时间'),tr('State','状态'),tr('Summary','摘要')],(result.value.runs||[]).map(run=>[run.scheduled_label,run.state,run.summary])));
  }
  function scheduleEditor(item) {
    const spec=item?.spec||{title:'',goal:'',machine_id:'',timezone:Intl.DateTimeFormat().resolvedOptions().timeZone,kind:'daily',at:'',every_minutes:60,time:'09:00',weekdays:[1,2,3,4,5],misfire:'coalesce',notify:'changes',enabled:true,profile:null,model:null,requirements:{tools:false,desktop:false,exact_budget:false,live_steering:false}};
    renderEditor(item?tr('Edit schedule','编辑计划'):tr('New schedule','新建计划'),{...spec,owner_ref:JSON.stringify([item?item._space_id:scope().space,item?item._assistant_id:scope().owner]),weekdays:spec.weekdays.join(',')},[
      ...(!item?[{name:'owner_ref',label:tr('Assistant','助手'),choices:(client.assistants||[]).map(owner=>[JSON.stringify([owner.space_id,owner.id]),`${owner.name} · ${owner.space_name||''}`])}]:[]),
      {name:'title',label:tr('Title','标题'),required:true},{name:'goal',label:tr('Goal','目标'),kind:'textarea',required:true},{name:'machine_id',label:tr('Machine ID','机器 ID')},
      {name:'timezone',label:tr('Time zone','时区'),required:true},{name:'kind',label:tr('Repeat','重复'),choices:[['once',tr('Once','一次')],['interval',tr('Interval','间隔')],['daily',tr('Daily','每日')],['weekly',tr('Weekly','每周')]]},
      {name:'at',label:tr('Date and time','日期与时间'),help:'YYYY-MM-DD HH:MM'},{name:'every_minutes',label:tr('Interval in minutes','间隔分钟'),kind:'number',parse:Number},{name:'time',label:tr('Time','时间'),kind:'time'},
      {name:'weekdays',label:tr('Weekdays','星期'),help:tr('1 = Monday, 7 = Sunday; comma separated.','1 为周一，7 为周日；以逗号分隔。')},
      {name:'misfire',label:tr('Missed runs','错过执行'),choices:[['coalesce',tr('Run once','合并执行一次')],['skip',tr('Skip','跳过')]]},
      {name:'notify',label:tr('Notifications','通知'),choices:[['changes',tr('Changes','发生变化')],['all',tr('Every run','每次执行')],['failures',tr('Failures','仅失败')]]},{name:'enabled',label:tr('Enabled','启用'),kind:'checkbox'},
    ],(values,context)=>{const {owner_ref,...specValues}=values;const [space,owner]=JSON.parse(owner_ref);return request('/api/v1/schedules',{...context,space,owner,method:'POST',body:{request_id:context.request_id,action:item?'update':'create',schedule_id:item?.id||'',spec:{...specValues,weekdays:values.weekdays.split(',').filter(Boolean).map(Number)}}});},{capturedScope:item?{space:item._space_id,owner:item._assistant_id}:scope()});
  }
  async function usage(root, token) {
    const controls=toolbar();for(const days of [7,30,90])controls.append(action(tr(`${days} days`,`${days} 天`),()=>{filter.usageDays=days;filter.usageOffset=0;show('usage');},{primary:filter.usageDays===days}));root.append(controls);
    const timezone=Intl.DateTimeFormat().resolvedOptions().timeZone;
    if(client.desktop&&(client.spaces||[]).length>1){const location=field(tr('Location','位置'),filter.usageSpace,{choices:[['all',tr('All Spaces','全部 Space')],...(client.spaces||[]).map(space=>[space.id,space.name])]});location.input.onchange=()=>{filter.usageSpace=location.input.value;filter.usageOffset=0;show('usage');};root.append(location.row);}
    const pages=await across(`/api/v1/usage/summary?${new URLSearchParams({days:filter.usageDays,timezone,scope:'',offset:filter.usageOffset})}`,token,{spaceID:filter.usageSpace});if(!gate.current(token))return;const data=pages.length===1?{...pages[0].value,spaces:pages}:combineUsage(pages);
    const total=data.total||{},metrics=n('div','usage-metrics');
    for(const [label,key] of [[tr('Input','输入'),'input'],[tr('Output','输出'),'output'],[tr('Cache','缓存'),'cache'],[tr('Requests','请求'),'requests']]){const item=n('div','metric');item.append(n('span','hint',label),n('strong','',Number(total[key]||0).toLocaleString(locale())));metrics.append(item);}root.append(metrics);
    const chart=n('div','usage-chart');chart.setAttribute('role','img');chart.setAttribute('aria-label',tr('Daily token usage','每日 token 用量'));
    const maximum=Math.max(1,...(data.buckets||[]).map(b=>b.input+b.output));
    for(const bucket of data.buckets||[]){const column=n('div','chart-column'),bar=n('div','chart-bar');bar.style.height=`${Math.max(0,(bucket.input+bucket.output)/maximum*140)}px`;bar.title=`${bucket.label}: ${bucket.input+bucket.output}`;column.append(bar,n('small','',bucket.label));chart.append(column);}root.append(chart);
    for(const page of pages) {
      const value=page.value,owner={space:page.space.id,owner:''};
      const policy=value.quota?.policy||await request('/api/v1/usage/policy',{...owner,signal:token.signal});if(!gate.current(token))return;
      const allowance=section(`${tr('Token allowance','Token 配额')} · ${page.space.name}`);allowance.append(n('p','hint',value.quota?.active?`${Number(value.quota.remaining).toLocaleString(locale())} ${tr('remaining','剩余')} · ${value.quota.reset_label||''}`:tr('No active token limit','未启用 token 限额')),action(tr('Edit allowance','编辑配额'),()=>policyEditor(policy,owner),{disabled:page.stale}));root.append(allowance);
      if(value.waiting){const waiting=section(tr('Waiting for allowance','等待配额'));waiting.append(n('p','hint',value.waiting.provider||''));for(const name of ['resume','cancel']){const button=action(tr(name==='resume'?'Resume':'Cancel waiting',name==='resume'?'恢复':'取消等待'),()=>operation(button,()=>request(`/api/v1/usage/${name}`,{...owner,method:'POST',body:{request_id:uid()}})),{disabled:page.stale});waiting.append(button);}root.append(waiting);}
    }
    root.append(section(tr('Model requests','模型请求')),table([tr('Time','时间'),tr('Model','模型'),tr('Input','输入'),tr('Output','输出'),tr('State','状态')],(data.requests||[]).map(record=>[record.label,record.model,record.input,record.output,record.state])));
    if(data.next_offset!=null)root.append(action(tr('Next page','下一页'),()=>{filter.usageOffset=data.next_offset;show('usage');}));
    root.append(section(tr('Tools','工具')),table([tr('Tool','工具'),tr('Calls','调用'),tr('Failed','失败'),tr('Duration (ms)','耗时（毫秒）')],(data.tools||[]).map(tool=>[tool.name,tool.calls,tool.failed,tool.duration_ms])));
    root.append(section(tr('Context compaction','上下文压缩')),table([tr('Time','时间'),tr('State','状态'),tr('Before','之前'),tr('After','之后')],(data.compactions||[]).map(record=>[record.label,record.state,record.tokens_before,record.tokens_after])));
  }
  function policyEditor(policy,owner) {
    renderEditor(tr('Token allowance','Token 配额'),policy,[
      {name:'period',label:tr('Period','周期'),choices:[['none',tr('Unlimited','无限制')],['day',tr('Daily','每日')],['week',tr('Weekly','每周')]]},
      {name:'limit',label:tr('Token limit','Token 上限'),kind:'number',parse:Number,required:true},{name:'timezone',label:tr('Time zone','时区'),required:true},{name:'auto_resume',label:tr('Automatically resume','自动恢复'),kind:'checkbox'},
    ],(values,context)=>request('/api/v1/usage/policy',{...context,method:'POST',body:values}),{capturedScope:owner});
  }
  async function machines(root, token) {
    const pages=await across('/api/v1/machines',token);if(!gate.current(token))return;const result={items:pages.flatMap(page=>ownedItems(page.value,page.space,page.stale,page.owner))};
    if(!list(result).length)empty(root,tr('No machines','暂无机器'));
    for(const machine of list(result)) {
      const group=section(machine.name||machine.runtime_id||machine.id,`${machine._space_name||''} · ${machine.os||''} · ${machine._stale?tr('Stale','已过期'):machine.connected?tr('Connected','已连接'):tr('Offline','离线')}`);
      const capabilities=machine.capabilities||{};group.append(n('p','hint',Object.entries(capabilities).filter(([,value])=>value===true).map(([key])=>key).join(' · ')));
      const policy=machine.policy||{execution_mode:'sandbox',host_access:'ask'};
      const mode=field(tr('Execution mode','执行模式'),policy.execution_mode,{choices:[['sandbox','Sandbox'],['host','Host']]}),hostAccess=field(tr('Host access','Host 访问'),policy.host_access,{choices:[['ask',tr('Ask each time','每次询问')],['allow',tr('Allow','允许')],['deny',tr('Deny','拒绝')]]});group.append(mode.row,hostAccess.row);
      const save=action(tr('Save permissions','保存权限'),()=>operation(save,()=>request('/api/v1/permissions',{method:'POST',space:machine._space_id,owner:'',body:{action:'configure',machine_id:machine.runtime_id||machine.id,machine_policy:{execution_mode:mode.input.value,host_access:hostAccess.input.value}}})),{disabled:!machine.connected||machine._stale});group.append(save);
      if(machine.sandbox) {const sandbox=machine.sandbox;group.append(n('h3','','Sandbox'),n('p','hint',`${sandbox.state?.phase||sandbox.state||''}${sandbox.quarantined?' · '+tr('Quarantined','已隔离'):''}`));
        const actions=toolbar();for(const name of ['start','stop','rebuild','recover']){const button=action(tr({start:'Start',stop:'Stop',rebuild:'Rebuild',recover:'Recover'}[name],{start:'启动',stop:'停止',rebuild:'重建',recover:'恢复'}[name]),()=>operation(button,()=>request('/api/v1/machines',{method:'POST',space:machine._space_id,owner:'',body:{action:name,machine_id:machine.id}})),{disabled:!machine.connected||machine._stale,danger:name==='rebuild'});actions.append(button);}group.append(actions);
        if(sandbox.resources) {group.append(n('p','hint',`${sandbox.resources.cpus} CPU · ${sandbox.resources.memory_gib} GiB · ${sandbox.resources.disk_gib} GiB`));group.append(action(tr('Configure resources','配置资源'),()=>resourceEditor(machine)));}
      }
      if(capabilities.desktop)group.append(action(tr('Open desktop','打开桌面'),()=>desktop(machine),{disabled:!machine.connected||machine._stale}));root.append(group);
    }
  }
  function resourceEditor(machine) {
    renderEditor(tr('Sandbox resources','Sandbox 资源'),machine.sandbox.resources,[
      {name:'cpus',label:'CPU',kind:'number',parse:Number,required:true},{name:'memory_gib',label:tr('Memory (GiB)','内存（GiB）'),kind:'number',parse:Number,required:true},{name:'disk_gib',label:tr('Disk (GiB)','磁盘（GiB）'),kind:'number',parse:Number,required:true},
    ],(values,context)=>request('/api/v1/machines',{...context,method:'POST',body:{action:'configure_resources',machine_id:machine.id,resources:values}}),{capturedScope:{space:machine._space_id,owner:''}});
  }
  async function desktop(machine) {
    if(machine._space_id&&machine._space_id!==client.spaces?.find(space=>space.kind==='local')?.id&&client.desktop){errorView(Error(tr('Remote desktop viewing is unavailable.','远端桌面查看暂不可用。')));return;}
    if(client.openDesktop){try{await client.openDesktop(machine.sandbox?.id||machine.id);}catch(reason){errorView(reason);}return;}
    const captured={space:machine._space_id,owner:''};let sessionPath;
    try {
      const result=await request('/api/v1/desktop/sessions',{...captured,method:'POST',body:{sandbox_id:machine.sandbox?.id||machine.id}});
      if(typeof result.path!=='string'||!/^\/desktop\/[A-Za-z0-9]+\/$/.test(result.path))throw Error(tr('Desktop transport returned an invalid display path.','桌面传输返回的显示路径无效。'));
      sessionPath=result.path;
      const root=body(),frame=n('iframe','desktop-frame');frame.src=sessionPath;frame.title=machine.name||machine.id;
      const close=action(tr('Close desktop','关闭桌面'),()=>operation(close,()=>request(`${sessionPath}api/close`,{...captured,method:'POST',body:{}}),()=>show('machines')));
      root.append(toolbar(close),frame);
    }catch(reason){if(sessionPath)request(`${sessionPath}api/close`,{...captured,method:'POST',body:{}}).catch(()=>{});errorView(reason);}
  }
  async function permissions() {
    if(!scope().owner||disposed)return;
    const token=approvalGate.next(),captured=scope();
    try {
      const value=await request('/api/v1/permissions',{...captured,signal:token.signal});if(!approvalGate.current(token)||disposed)return;
      const pending=(value.pending||[]).filter(item=>!item.request?.owner||item.request.owner===captured.owner);
      const fragment=document.createDocumentFragment();
      for(const approval of pending) {const box=n('section','approval-card');box.append(n('strong','',tr('Host access requested','请求 Host 访问')),n('p','hint',`${approval.request.machine_id} · ${approval.request.tool}`),details(tr('Arguments','参数'),approval.request.args));
        for(const name of ['allow_once','deny']){const button=action(tr(name==='allow_once'?'Allow once':'Deny',name==='allow_once'?'允许一次':'拒绝'),()=>operation(button,()=>request('/api/v1/permissions',{...captured,method:'POST',body:{id:approval.id,action:name}}),permissions),{primary:name==='allow_once'});box.append(button);}fragment.append(box);}
      approvalHost.replaceChildren(fragment);
    }catch(reason){if(approvalGate.current(token)&&reason.name!=='AbortError')notify(reason.message);}
  }
  async function show(next) {
    if(editor)return;
    route=next;heading(tr({tasks:filter.taskTab==='schedules'?'Schedules':'Tasks',machines:'Machines',usage:'Usage',settings:'Settings'}[next]||next,{tasks:filter.taskTab==='schedules'?'计划':'任务',machines:'机器',usage:'用量',settings:'设置'}[next]||next));
    const version=++pageVersion,token=gate.next(),root=body();
    root.append(n('p','hint',tr('Loading…','正在加载…')));
    try {root.replaceChildren();await ({settings,tasks,machines,usage}[next]||settings)(root,token);if(gate.current(token))cached.set(next,host.scrollTop);}
    catch(reason){if(gate.current(token)&&version===pageVersion&&!disposed)errorView(reason,root);}
  }
  const popstate=async event=>{
    if(!editor||event.state?.potEditor===editor.id)return;
    const current=editor;
    if(!await leave()){history.pushState({...history.state,potEditor:current.id},'',location.href);return;}
    if(current.returnChat)openChat();else show(current.caller||route||'settings');
  };
  window.addEventListener('popstate',popstate);
  const beforeUnload=event=>{if(editor?.session.dirty||editor?.session.pending){event.preventDefault();event.returnValue='';}};
  const preference=()=>{if(!editor&&route)show(route);};
  window.addEventListener('beforeunload',beforeUnload);window.addEventListener('pot:locale',preference);
  return {get title(){return title;},get editing(){return !!editor;},show,assistant,leave,back,permissions,
    refresh(){if(!editor&&route&&host.offsetParent)show(route);},
    dispose(){disposed=true;gate.close();approvalGate.close();window.removeEventListener('popstate',popstate);window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('pot:locale',preference);},
  };
}
