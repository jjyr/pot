import {translate} from '../locales/messages.mjs';
import {node as n,action} from './primitives.mjs';
import {icon} from './icons.mjs';
import {keyedRows,setPending} from './reconcile.mjs';

export function createProviderList({root,tr,request,scope,current,pick,edit,spaceEditor,refresh}) {
  let snapshot={},saving=false;
  root.append(n('p','hint',tr('Manage connections and default models for assistants.','管理助手的连接和默认模型。')));
  const header=n('div','page-toolbar provider-heading'),add=action(tr('+ Add Provider','＋ 添加提供商'),()=>pick(snapshot));add.setAttribute('aria-label',tr('Add connection','添加连接'));
  header.append(n('h2','page-heading',tr('Configured Providers','已配置的提供商')),add);root.append(header);
  const empty=n('p','page-empty',tr('Add an AI Provider to get started. The first Provider becomes the default automatically.','添加 AI 提供商以开始使用，第一个提供商将自动设为默认。'));
  const help=n('p','hint provider-default-help',tr('New assistants preselect the default Provider and its default model.','新助手将预选默认提供商及其默认模型。')),rows=n('div'),entries=new Map(),status=n('div','hint provider-defaults-status');status.setAttribute('role','status');status.setAttribute('aria-live','polite');root.append(empty,help,rows,status);empty.hidden=true;help.hidden=true;
  const spaces=n('section','page-section page-card'),spaceRows=n('div'),spaceEntries=new Map();spaces.append(n('h2','',tr('Spaces and connections','Space 与连接')),spaceRows,action(tr('Connect Space','连接 Space'),spaceEditor));root.append(spaces);
  function defaults(){for(const entry of entries.values()){const selected=entry.item.id===snapshot.default_provider_id;entry.badge.hidden=!selected;entry.default.disabled=saving||selected;entry.default.setAttribute('aria-pressed',String(selected));entry.default.setAttribute('aria-label',`${entry.item.name}: ${selected?tr('Current default Provider','当前默认提供商'):tr('Set as default Provider','设为默认提供商')}`);entry.default.title=selected?tr('Current default Provider','当前默认提供商'):tr('Set as default Provider','设为默认提供商');}}
  async function setDefault(profile){
    if(saving||profile.id===snapshot.default_provider_id)return;saving=true;defaults();status.setAttribute('role','status');status.className='hint provider-defaults-status';status.textContent=tr('Setting default Provider…','正在设置默认提供商…');
    try{await request('/api/v1/providers/default',{...scope,method:'POST',body:{provider_id:profile.id}});if(!current())return;snapshot.default_provider_id=profile.id;status.textContent=tr(`${profile.name} is now the default Provider.`,`已将 ${profile.name} 设为默认提供商。`);await refresh();}
    catch(error){if(current()){status.setAttribute('role','alert');status.classList.add('inline-error');status.replaceChildren(n('span','',error.message),action(tr('Retry','重试'),()=>setDefault(profile)));}}
    finally{saving=false;defaults();}
  }
  return data=>{
    snapshot=data.providers;const profiles=snapshot.profiles||[];empty.hidden=!!profiles.length;help.hidden=!profiles.length;
    keyedRows(rows,entries,profiles,{key:profile=>profile.id,create:()=>{
      const element=n('div','page-card provider-summary'),logo=n('span'),info=n('div','row-info'),name=n('span'),badge=n('span','provider-default-badge',tr('Default','默认')),heading=n('span','provider-name'),model=n('span','hint');heading.append(name,badge);info.append(heading,model);
      const entry={element,logo,name,badge,model};entry.default=action('',()=>setDefault(entry.item));entry.default.classList.add('icon-button','provider-default');entry.default.append(icon('star'));
      const configure=action(tr('Configure','配置'),()=>edit(entry.item,snapshot));configure.setAttribute('aria-label',tr('Edit','编辑'));element.append(logo,info,entry.default,configure);return entry;
    },update:(entry,profile)=>{entry.name.textContent=profile.name;entry.model.textContent=profile.model;entry.default.dataset.providerId=profile.id;const logo=(snapshot.directory||[]).find(value=>value.id===profile.model_settings?.catalog_provider)?.logo;if(entry.logoValue!==logo||!entry.logo.childNodes.length){entry.logoValue=logo;entry.logo.replaceChildren();if(logo?.startsWith('<svg')){const image=n('img','provider-logo');image.alt='';image.src=`data:image/svg+xml;charset=utf-8,${encodeURIComponent(logo)}`;entry.logo.append(image);}else entry.logo.append(icon('settings'));}}});defaults();
    spaces.hidden=!data.spaces;
    keyedRows(spaceRows,spaceEntries,data.spaces?.items||[],{key:space=>space.id,create:()=>{
      const element=n('div','list-row'),name=n('span'),status=n('span','hint'),buttons=n('span'),entry={element,name,status,buttons};element.append(name,status,buttons);
      for(const name of ['connect','disconnect','remove']){const button=action(tr({connect:'Connect',disconnect:'Disconnect',remove:'Remove'}[name],{connect:'连接',disconnect:'断开',remove:'移除'}[name]),async()=>{setPending(button,true);try{await request('/api/v1/spaces',{method:'POST',owner:'',space:'',body:{action:name,space_id:entry.item.id}});if(current())await refresh();}catch(error){if(current()){status.setAttribute('role','alert');status.classList.add('inline-error');status.textContent=error.message;}}finally{setPending(button,false);}});buttons.append(button);}return entry;
    },update:(entry,space)=>{entry.name.textContent=space.name;entry.status.textContent=space.connected?tr('Connected','已连接'):tr('Offline','离线');entry.buttons.hidden=space.kind!=='remote';}});
  };
}

export function createTaskSettings({root,tr,locale,configure,allowance}) {
  const execution=n('section','page-section page-card'),limits=n('section','page-section page-card'),rows=n('div'),quotas=n('div'),entries=new Map(),quotaEntries=new Map();let data;
  const executionStatus=n('p','hint',translate(locale(),'loading.pending')),quotaStatus=n('p','hint',translate(locale(),'loading.pending'));
  execution.append(n('h2','',tr('Task Configuration','任务配置')),executionStatus,rows);limits.append(n('h2','',tr('Provider allowances','提供商配额')),quotaStatus,quotas);root.append(execution,limits);
  return value=>{data=value;executionStatus.hidden=!!data.providers&&!!data.policy;quotaStatus.hidden=!!data.allowances;execution.setAttribute('aria-busy',String(!data.providers||!data.policy));limits.setAttribute('aria-busy',String(!data.allowances));keyedRows(rows,entries,data.providers?.profiles||[],{key:profile=>profile.id,create:()=>{const element=n('div','list-row'),name=n('span','row-info'),model=n('span','hint'),entry={element,name,model};element.append(name,model,action(tr('Configure','配置'),()=>configure(entry.item,data.policy)));return entry;},update:(entry,profile)=>{entry.name.textContent=profile.name;entry.model.textContent=profile.model;entry.element.querySelector('button').disabled=!data.policy;}});
    keyedRows(quotas,quotaEntries,data.allowances?.providers||[],{key:quota=>quota.provider,create:()=>{const element=n('div','list-row'),name=n('span','row-info'),remaining=n('span','hint'),entry={element,name,remaining};element.append(name,remaining,action(tr('Edit allowance','编辑配额'),()=>allowance(entry.item)));return entry;},update:(entry,quota)=>{entry.name.textContent=(data.providers?.profiles||[]).find(profile=>profile.provider===quota.provider)?.name||quota.provider;entry.remaining.textContent=quota.active?`${Number(quota.remaining).toLocaleString()} ${tr('remaining','剩余')}`:tr('Unlimited','无限制');}});
  };
}
