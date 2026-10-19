import {node} from './primitives.mjs';

// The creation request is the durable appearance seed. Older directory entries
// without it keep the stable assistant identity fallback. Names never affect it.
export function assistantAvatarKey(item) {
  return item?.request_id || item?.id || 'main';
}

const foreground=['#276451','#904e39','#685083','#3a6590','#8a6928','#8b4a68','#40546c','#5a6f3c'];
const background=['#dce9e2','#f5ddd2','#e5ddf1','#dce8f4','#f4e8c8','#f1dce6','#dde4ec','#e3e8d9'];
const palettes=new Map();
let instance=0;

export function avatar(size = 32, key = '', image = '') {
  const result = node('span', 'avatar');
  result.style.width = result.style.height = `${size}px`;
  result.setAttribute('aria-hidden', 'true');
  if(image?.startsWith('data:image/png;base64,')){const photo=node('img','avatar-image');photo.src=image;photo.alt='';result.append(photo);return result;}
  const mark=node('span','avatar-mark');result.append(mark);
  // Async painting belongs to this decorative node. Reconciliation must insert
  // it rather than copy its still-unpainted projection onto a retained node.
  result.dataset.viewKey=palettes.has(key)?`avatar:${key}`:`avatar-pending:${++instance}`;
  const paint=hash=>{mark.style.background=foreground[hash[0]%8];result.style.background=background[hash[1]%8];};
  if(palettes.has(key))paint(palettes.get(key));
  else if(key&&crypto.subtle)crypto.subtle.digest('SHA-256',new TextEncoder().encode(key)).then(value=>{
    const hash=new Uint8Array(value).slice(0,2);
    if(palettes.size>=256)palettes.delete(palettes.keys().next().value);
    palettes.set(key,hash);paint(hash);
  });
  return result;
}
