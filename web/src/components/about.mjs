import {node,details,section} from './primitives.mjs';

export async function renderAbout({client,root,scope,locale}) {
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en;
  const captured={...scope,owner:''};
  const card=section(tr('About','关于'));card.classList.add('page-card');root.append(card);
  card.append(node('p','','Pot'));
  const font=node('a','','IBM Plex Sans — OFL');font.href=new URL('../assets/IBMPlexSans-OFL.txt',import.meta.url);font.target='_blank';font.rel='noopener';card.append(font);
  try {
    const about=await client.request('/api/v1/about',captured);
    if(!root.isConnected||!card.isConnected)return;
    card.prepend(node('p','hint',`${about.version||tr('Development build','开发版本')} · ${about.revision||''}${about.go_version?` · ${about.go_version}`:''}`));
    for(const license of about.licenses||[])card.append(details(license.name,license.text));
    if(!about.notices_available)card.append(node('p','hint',tr('The complete dependency notices are included in packaged builds.','完整依赖许可随打包版本提供。')));
  }catch(reason){
    if(root.isConnected&&card.isConnected)card.append(node('p','hint',tr('Build information and dependency notices are unavailable in this preview.','此预览中暂无构建信息和依赖许可。')));
  }
}
