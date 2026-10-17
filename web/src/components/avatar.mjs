import {node} from './primitives.mjs';

export function avatar(size = 32, key = '') {
  const result = node('span', 'avatar');
  result.style.width = result.style.height = `${size}px`;
  result.setAttribute('aria-hidden', 'true');
  const mark=node('span','avatar-mark');result.append(mark);
  if(key&&crypto.subtle)crypto.subtle.digest('SHA-256',new TextEncoder().encode(key)).then(value=>{const hash=new Uint8Array(value),foreground=['#276451','#904e39','#685083','#3a6590','#8a6928','#8b4a68','#40546c','#5a6f3c'],background=['#dce9e2','#f5ddd2','#e5ddf1','#dce8f4','#f4e8c8','#f1dce6','#dde4ec','#e3e8d9'];mark.style.background=foreground[hash[0]%8];result.style.background=background[hash[1]%8];});
  return result;
}
