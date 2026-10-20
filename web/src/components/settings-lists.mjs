import {translate} from '../locales/messages.mjs';
import {node as n,action} from './primitives.mjs';
import {createProviderAllowances} from './provider-allowances.mjs';
import {icon} from './icons.mjs';
import {keyedRows} from './reconcile.mjs';

export function createProviderList({root,tr,locale,request,scope,current,pick,edit,refresh,allowanceMemory=new Map(),allowanceMemoryKey=''}) {
  const t=key=>translate(locale(),'providers.'+key);
  let snapshot={},saving=false;
  root.append(n('p','hint',t('manage')));
  const header=n('div','page-toolbar provider-heading'),add=action(t('addAction'),()=>pick(snapshot));add.setAttribute('aria-label',t('add'));
  header.append(n('h2','page-heading',t('configured')),add);root.append(header);
  const empty=n('p','page-empty',t('empty'));
  const help=n('p','hint provider-default-help',t('defaultHelp')),rows=n('div'),entries=new Map(),status=n('div','hint provider-defaults-status');status.setAttribute('role','status');status.setAttribute('aria-live','polite');root.append(empty,help,rows,status);empty.hidden=true;help.hidden=true;
  function defaults(){for(const entry of entries.values()){const selected=entry.item.id===snapshot.default_provider_id;entry.badge.hidden=!selected;entry.default.disabled=saving||selected;entry.default.setAttribute('aria-pressed',String(selected));entry.default.setAttribute('aria-label',`${entry.item.name}: ${selected?t('currentDefault'):t('setDefault')}`);entry.default.title=selected?t('currentDefault'):t('setDefault');}}
  async function setDefault(profile){
    if(saving||profile.id===snapshot.default_provider_id)return;saving=true;defaults();status.setAttribute('role','status');status.className='hint provider-defaults-status';status.textContent=t('settingDefault');
    try{await request('/api/v1/providers/default',{...scope,method:'POST',body:{provider_id:profile.id}});if(!current())return;snapshot.default_provider_id=profile.id;status.textContent=t('defaultSaved').replace('{name}',profile.name);await refresh();}
    catch(error){if(current()){status.setAttribute('role','alert');status.classList.add('inline-error');status.replaceChildren(n('span','',error.message),action(t('retry'),()=>setDefault(profile)));}}
    finally{saving=false;defaults();}
  }
  const update=data=>{
    add.disabled=false;snapshot=data.providers;const profiles=snapshot.profiles||[];empty.hidden=!!profiles.length;help.hidden=!profiles.length;
    keyedRows(rows,entries,profiles,{key:profile=>profile.id,create:()=>{
      const element=n('div','page-card provider-summary-card'),main=n('div','provider-summary'),logo=n('span'),info=n('div','row-info'),name=n('span'),badge=n('span','provider-default-badge',t('default')),heading=n('span','provider-name'),model=n('span','hint');heading.append(name,badge);info.append(heading,model);
      const entry={element,main,logo,name,badge,model};entry.dispose=()=>entry.allowances?.dispose();entry.default=action('',()=>setDefault(entry.item));entry.default.classList.add('icon-button','provider-default');entry.default.append(icon('star'));
      const configure=action(t('configure'),()=>edit(entry.item,snapshot));configure.setAttribute('aria-label',t('edit'));main.append(logo,info,entry.default,configure);element.append(main);return entry;
    },update:(entry,profile)=>{entry.name.textContent=profile.name;entry.model.textContent=profile.model;entry.default.dataset.providerId=profile.id;entry.connection=(snapshot.connections||[]).find(value=>value.profile_id===profile.id);const logo=entry.connection?.icon;entry.main.classList.toggle('provider-unavailable',entry.connection?.available===false);if(entry.connection?.available===false){entry.model.textContent=profile.model+' · '+t('unavailable');}if(entry.connection?.capabilities?.allowances&&!entry.allowances){const key=JSON.stringify([scope.space,allowanceMemoryKey,profile.id]);if(!allowanceMemory.has(key))allowanceMemory.set(key,{});entry.allowances=createProviderAllowances({root:entry.element,request,scope,profileID:profile.id,locale,current,memory:allowanceMemory.get(key),available:()=>entry.connection?.available!==false,identity:()=>[entry.connection?.provider_id,entry.connection?.type_id,entry.item.revision,entry.connection?.endpoint,entry.connection?.protocol,entry.connection?.available!==false],binding:()=>entry.connection?.plugin_id||entry.connection?.type_id||''});};if(!entry.connection?.capabilities?.allowances&&entry.allowances){entry.allowances.remove();entry.allowances=null;}entry.allowances?.();if(entry.logoValue!==logo||!entry.logo.childNodes.length){entry.logoValue=logo;entry.logo.replaceChildren();if(logo){const image=n('img','provider-logo');image.alt='';image.src=logo;entry.logo.append(image);}else entry.logo.append(icon('settings'));}}});defaults();

  };
  update.invalidate=()=>{add.disabled=true;for(const type of snapshot.provider_types||[])type.available=false;for(const entry of entries.values()){if(entry.connection)entry.connection.available=false;entry.allowances?.();}};
  return update;
}

export function createTaskSettings({root,tr,locale,configure,allowance}) {
  const execution=n('section','page-section page-card'),limits=n('section','page-section page-card'),rows=n('div'),quotas=n('div'),entries=new Map(),quotaEntries=new Map();let data;
  const executionStatus=n('p','hint',translate(locale(),'loading.pending')),quotaStatus=n('p','hint',translate(locale(),'loading.pending'));
  execution.append(n('h2','',tr('Task Configuration','任务配置')),executionStatus,rows);limits.append(n('h2','',tr('Provider allowances','提供商配额')),quotaStatus,quotas);root.append(execution,limits);
  return value=>{data=value;executionStatus.hidden=!!data.providers&&!!data.policy;quotaStatus.hidden=!!data.allowances;execution.setAttribute('aria-busy',String(!data.providers||!data.policy));limits.setAttribute('aria-busy',String(!data.allowances));keyedRows(rows,entries,data.providers?.profiles||[],{key:profile=>profile.id,create:()=>{const element=n('div','list-row'),name=n('span','row-info'),model=n('span','hint'),entry={element,name,model};element.append(name,model,action(tr('Configure','配置'),()=>configure(entry.item,data.policy)));return entry;},update:(entry,profile)=>{entry.name.textContent=profile.name;entry.model.textContent=profile.model;entry.element.querySelector('button').disabled=!data.policy;}});
    keyedRows(quotas,quotaEntries,data.allowances?.providers||[],{key:quota=>quota.provider,create:()=>{const element=n('div','list-row'),name=n('span','row-info'),remaining=n('span','hint'),entry={element,name,remaining};element.append(name,remaining,action(tr('Edit allowance','编辑配额'),()=>allowance(entry.item)));return entry;},update:(entry,quota)=>{entry.name.textContent=(data.providers?.profiles||[]).find(profile=>profile.provider===quota.provider)?.name||quota.provider;entry.remaining.textContent=quota.active?`${Number(quota.remaining).toLocaleString()} ${tr('remaining','剩余')}`:tr('Unlimited','无限制');}});
  };
}
