import {translate} from '../locales/messages.mjs';
import {icon} from './icons.mjs';
import {node,action,details,section} from './primitives.mjs';

export async function renderAbout({client,root,scope,locale,updates,signal}) {
  const t=key=>translate(locale(),key);
  const captured={...scope,owner:''};
  const card=section(t('about.title'));card.classList.add('page-card');root.append(card);
  card.append(node('p','','Pot'));
  const label=t('about.notices'),dialog=node('dialog','preferences about-notices');dialog.setAttribute('aria-label',label);
  const heading=node('div','preferences-heading'),close=action(t('updates.close'),()=>dialog.close());close.autofocus=true;
  heading.append(node('h2','',label),close);
  const notices=node('div','about-notices-body'),loading=node('p','hint',t('loading'));
  const font=node('a','','IBM Plex Sans — OFL');font.href=new URL('../assets/IBMPlexSans-OFL.txt',import.meta.url);font.target='_blank';font.rel='noopener';notices.append(font,loading);
  dialog.append(heading,notices);
  const open=action(label,()=>dialog.showModal());open.classList.add('about-notices-trigger');open.setAttribute('aria-haspopup','dialog');card.append(open,dialog);
  try {
    const about=await (client.read||client.request)('/api/v1/about',captured);
    if(!root.isConnected||!card.isConnected)return;
    const version=node('div','about-version');version.append(node('span','hint',`${about.version||t('about.development')} · ${about.revision||''}${about.go_version?` · ${about.go_version}`:''}`));
    card.prepend(version);
    if(updates){
      const check=action(t('updates.check'),()=>updates.check(captured));check.className='icon-button update-check';check.replaceChildren(icon('refresh'));version.append(check);
      const result=node('p','hint update-check-status');result.setAttribute('role','status');card.insertBefore(result,version.nextSibling);
      const stop=updates.watch(captured,value=>{if(!root.isConnected||signal?.aborted)return;check.disabled=value.checking;check.setAttribute('aria-busy',String(value.checking));check.setAttribute('aria-label',t('updates.check'));check.title=t('updates.check');result.textContent=value.feedback?t(`updates.${value.feedback}`):'';});
      signal?.addEventListener('abort',()=>{stop();check.disabled=false;check.setAttribute('aria-busy','false');},{once:true});
      await updates.refresh(captured);
    }
    loading.remove();
    for(const license of about.licenses||[])notices.append(details(license.name,license.text));
    if(!about.notices_available)notices.append(node('p','hint',t('about.packagedNotices')));
  }catch(reason){
    if(root.isConnected&&card.isConnected){
      card.prepend(node('p','hint',t('about.unavailable')));
      loading.textContent=t('about.noticesUnavailable');
    }
  }
}
