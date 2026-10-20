import {node} from './primitives.mjs';

// All profile avatars, including generated defaults, are persisted PNG images.
// Unloaded identities use a neutral surface until their saved image arrives.
export function avatar(size = 32, image = '') {
  const result = node('span', 'avatar');
  result.style.width = result.style.height = `${size}px`;
  result.setAttribute('aria-hidden', 'true');
  if(!image?.startsWith('data:image/png;base64,'))return result;
  const photo = node('img', 'avatar-image');
  photo.src = image;
  photo.alt = '';
  result.append(photo);
  return result;
}

// Normalize locally before sending the small, bounded PNG through the existing
// profile endpoint. The backend independently validates its size and pixels.
export async function avatarFromFile(file) {
  if(!file||!['image/png','image/jpeg','image/webp'].includes(file.type)||!file.size||file.size>8*1024*1024)throw Error('Invalid avatar source');
  const image=await createImageBitmap(file);
  try{
    if(image.width>8192||image.height>8192||image.width*image.height>32*1024*1024)throw Error('Avatar dimensions exceed limit');
    const side=Math.min(image.width,image.height),canvas=document.createElement('canvas');canvas.width=canvas.height=128;
    canvas.getContext('2d').drawImage(image,(image.width-side)/2,(image.height-side)/2,side,side,0,0,128,128);
    return canvas.toDataURL('image/png');
  }finally{image.close();}
}
