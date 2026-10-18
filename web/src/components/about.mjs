import {node,action,details,section} from './primitives.mjs';

export async function renderAbout({client,root,scope,locale}) {
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en;
  const captured={...scope,owner:''};
  const card=section(tr('About','关于'));card.classList.add('page-card');root.append(card);
  card.append(node('p','','Pot'));
  const label=tr('Copyright notices','版权声明'),dialog=node('dialog','preferences about-notices');dialog.setAttribute('aria-label',label);
  const heading=node('div','preferences-heading'),close=action(tr('Close','关闭'),()=>dialog.close());close.autofocus=true;
  heading.append(node('h2','',label),close);
  const notices=node('div','about-notices-body'),loading=node('p','hint',tr('Loading…','正在加载…'));
  const font=node('a','','IBM Plex Sans — OFL');font.href=new URL('../assets/IBMPlexSans-OFL.txt',import.meta.url);font.target='_blank';font.rel='noopener';notices.append(font,loading);
  dialog.append(heading,notices);
  const open=action(label,()=>dialog.showModal());open.classList.add('about-notices-trigger');open.setAttribute('aria-haspopup','dialog');card.append(open,dialog);
  try {
    const about=await (client.read||client.request)('/api/v1/about',captured);
    if(!root.isConnected||!card.isConnected)return;
    card.prepend(node('p','hint',`${about.version||tr('Development build','开发版本')} · ${about.revision||''}${about.go_version?` · ${about.go_version}`:''}`));
    loading.remove();
    for(const license of about.licenses||[])notices.append(details(license.name,license.text));
    if(!about.notices_available)notices.append(node('p','hint',tr('The complete dependency notices are included in packaged builds.','完整依赖许可随打包版本提供。')));
  }catch(reason){
    if(root.isConnected&&card.isConnected){
      card.prepend(node('p','hint',tr('Build information is unavailable in this preview.','此预览中暂无构建信息。')));
      loading.textContent=tr('Dependency notices are unavailable in this preview.','此预览中暂无依赖许可。');
    }
  }
}
