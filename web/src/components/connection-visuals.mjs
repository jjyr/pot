import {node as n} from './primitives.mjs';

// Product brand assets, shared by connection previews and standalone hosts.
export function connectionBrand(kind='pot') {
  const mark=n('span','connection-brand');mark.setAttribute('aria-hidden','true');
  for(const [asset,theme] of kind==='codex'?[['Codex.png','light'],['CodexDark.png','dark']]:[['PotCompanion.png','both']]) {
    const image=n('img','connection-brand-'+theme);image.alt='';image.src=new URL('../assets/'+asset,import.meta.url).href;mark.append(image);
  }
  return mark;
}
