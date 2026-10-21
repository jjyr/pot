import {node as n} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

export const browserTimezone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || '';
export const timeRule = values => ({kind:values.kind,cron:values.cron||'',timezone:values.timezone,at:values.at||'',every_minutes:Number(values.every_minutes)||0});

// Preview never mutates a schedule. Both abort and generation checks are needed:
// remote/native transports may finish a request after it has been cancelled.
export function connectScheduleEditor({view,request,locale,current,definitions,machineInfo}) {
  const {controls,session,lifecycle,form}=view,t=key=>translate(locale(),`schedule.${key}`);
  const preview=n('section','schedule-preview'),title=n('p'),content=n('div'),diagnostic=n('details','detail-disclosure'),summary=n('summary'),raw=n('pre');
  preview.setAttribute('aria-live','polite');diagnostic.append(summary,raw);preview.append(title,content,diagnostic);
  controls.get('cron').closest('.form-row').after(preview);
  const errors=new Map();
  for(const name of ['timezone','cron','at','every_minutes','kind']){
    const input=controls.get(name),error=n('small','inline-error');error.id=`schedule-${name}-error`;error.setAttribute('role','alert');
    input.setAttribute('aria-describedby',error.id);input.parentElement.append(error);errors.set(name,error);
  }
  let timer,controller,generation=0,result=null,state='loading';
  let machineGeneration=0;
  async function loadMachines(){
    const input=controls.get('machine_id');if(!input||!machineInfo)return;
    const own=++machineGeneration,[space]=JSON.parse(session.values.owner_ref),selected=session.values.machine_id||'';
    input.disabled=true;
    const machines=await machineInfo.load({space});if(!current()||own!==machineGeneration)return;
    const choices=[['',t('defaultMachine')],...machines.map(machine=>[machine.runtime_id||machine.id,machineInfo.label(machine.runtime_id||machine.id,{space},locale())])];
    if(selected&&!choices.some(([id])=>id===selected))choices.push([selected,translate(locale(),'machine.unavailable')]);
    input.replaceChildren(...choices.map(([id,label])=>{const option=n('option','',label);option.value=id;return option;}));input.value=selected;input.disabled=false;
  }
  controls.get('owner_ref')?.addEventListener('input',()=>{session.values.machine_id='';loadMachines();});
  const visible=()=>{
    const kind=session.values.kind;
    for(const [name,expected] of [['cron','cron'],['at','once'],['every_minutes','interval']]){
      const input=controls.get(name);input.closest('.form-row').hidden=kind!==expected;input.required=kind===expected;
    }
  };
  const render=()=>{
    title.textContent=t('preview');summary.textContent=t('diagnostics');raw.textContent=result?.diagnostic||'';diagnostic.hidden=!raw.textContent;
    for(const [name,error]of errors){const invalid=result?.field===name;error.textContent=invalid?t(result.code==='timezone'?'timezoneError':'ruleError'):'';controls.get(name).setAttribute('aria-invalid',String(invalid));}
    content.replaceChildren();
    if(state==='loading')content.append(n('p','hint',t('loading')));
    else if(state==='failed')content.append(n('p','inline-error',t('previewFailed')));
    else if(result?.field)content.append(n('p','hint',t('fixRule')));
    else if(!result?.times?.length)content.append(n('p','inline-error',t('noFuture')));
    else {
      const list=n('ol');for(const instant of result.times)list.append(n('li','',new Intl.DateTimeFormat(locale(),{dateStyle:'medium',timeStyle:'long',timeZone:result.timezone}).format(instant)));
      content.append(n('code','',session.values.kind==='cron'?session.values.cron:t(session.values.kind)),n('p','hint',result.timezone),list);
    }
  };
  const load=async values=>{
    const own=++generation;controller?.abort();controller=new AbortController();state='loading';result=null;render();
    const [space,owner]=JSON.parse(values.owner_ref);
    try{
      const next=await request('/api/v1/schedules/preview',{method:'POST',body:timeRule(values),space,owner,signal:controller.signal});
      if(!current()||own!==generation)return null;
      result=next;state='ready';render();return next;
    }catch(reason){if(current()&&own===generation){state='failed';render();}return null;}
  };
  const update=()=>{visible();++generation;controller?.abort();clearTimeout(timer);result=null;state='loading';render();timer=setTimeout(()=>{if(current())load(session.values);},250);};
  for(const name of ['kind','cron','timezone','at','every_minutes','owner_ref'])controls.get(name)?.addEventListener('input',update);
  lifecycle.dispose=()=>{++machineGeneration;++generation;clearTimeout(timer);controller?.abort();};
  lifecycle.locale=()=>{
    for(const definition of definitions){const input=controls.get(definition.name);if(!input)continue;const row=input.closest('.form-row');row.querySelector('.form-label').textContent=t(definition.key);input.setAttribute('aria-label',t(definition.key));
      if(definition.helpKey)row.querySelector('.hint').textContent=t(definition.helpKey);
      if(definition.choiceKeys)for(const option of input.options)option.textContent=t(definition.choiceKeys[option.value]);
    }
    const buttons=form.querySelectorAll('.editor-actions button');buttons[0].textContent=t('cancel');buttons[1].textContent=t('save');loadMachines();render();
  };
  visible();load(session.values);loadMachines();
  return {async validate(values){
    clearTimeout(timer);const next=await load(values);
    if(!next||next.field||!next.times.length){
      if(next?.field)controls.get(next.field)?.focus();
      throw Object.assign(Error(t('ruleError')),{uiKey:`schedule.${!next?'previewFailed':next.field?(next.code==='timezone'?'timezoneError':'ruleError'):'noFuture'}`,diagnostic:next?.diagnostic});
    }
  }};
}
