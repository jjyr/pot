import {node} from './primitives.mjs';

// All profile avatars, including generated defaults, are persisted PNG images.
// The bundled Companion is only a placeholder for an unloaded/preview identity.
export function avatar(size = 32, image = '') {
  const result = node('span', 'avatar');
  result.style.width = result.style.height = `${size}px`;
  result.setAttribute('aria-hidden', 'true');
  const photo = node('img', 'avatar-image');
  photo.src = image?.startsWith('data:image/png;base64,') ? image : new URL('../assets/PotCompanion.png', import.meta.url).href;
  photo.alt = '';
  result.append(photo);
  return result;
}
