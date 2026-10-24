import {linkDisplayName} from '../state/machine-identity.mjs';
import {translate} from '../locales/messages.mjs';
import {icon} from './icons.mjs';
import {node as n} from './primitives.mjs';

// Product brand assets, shared by connection previews and standalone hosts.
export function connectionBrand(kind='pot') {
  const mark=n('span','connection-brand');mark.setAttribute('aria-hidden','true');
  if(!['pot','codex','antigravity'].includes(kind)){mark.append(icon('plug'));return mark;}
  if(kind==='antigravity'){const image=n('img','connection-brand-both');image.alt='';image.src=new URL('../assets/antigravity.svg',import.meta.url).href;mark.append(image);return mark;}
  for(const [asset,theme] of kind==='codex'?[['Codex.png','light'],['CodexDark.png','dark']]:[['PotCompanion.png','both']]) {
    const image=n('img','connection-brand-'+theme);image.alt='';image.src=new URL('../assets/'+asset,import.meta.url).href;mark.append(image);
  }
  return mark;
}

// Source is descriptive: selecting it never changes task or conversation ownership.
export function agentSource(source,locale) {
  if(!source||!['pot','codex','antigravity'].includes(source.kind))return null;
  const label=translate(locale,source.kind==='antigravity'?'agentLinks.sourceAntigravity':source.kind==='codex'?'agentLinks.sourceCodex':'agentLinks.sourcePot');
  const name=linkDisplayName({peer:source}),mark=n('span','agent-source');mark.title=name&&name!==label?`${label} · ${name}`:label;mark.setAttribute('aria-label',mark.title);
  mark.append(connectionBrand(source.kind));
  if(source.kind==='pot')mark.append(icon('cloud'));
  return mark;
}

export const taskAgentSource=(task,locale)=>agentSource(task.agent_link||(['codex','pot','antigravity'].includes(task.executor_icon)?{kind:task.executor_icon}:null),locale);
