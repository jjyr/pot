import {translate} from '../locales/messages.mjs';
import {openImageViewer} from './image-viewer.mjs';

const node=(tag,cls='')=>{const element=document.createElement(tag);element.className=cls;return element;};
function registerImage() {
  if(customElements.get('pot-file-image'))return;
  customElements.define('pot-file-image',class extends HTMLElement {
    generation=0;
    connectedCallback() {
      const generation=++this.generation;
      const t=key=>translate(this.ownerDocument.documentElement.lang,`markdown.${key}`);
      const preview=node('button','file-image-preview control'),image=node('img'),status=node('span','file-image-status'),download=node('button','file-image-download control');
      preview.type=download.type='button';preview.disabled=true;image.alt=this.label;image.decoding='async';image.hidden=true;preview.append(image);status.setAttribute('role','status');
      preview.setAttribute('aria-haspopup','dialog');
      this.replaceChildren(preview,status,download);this.dataset.state='loading';this.setAttribute('aria-busy','true');
      let failure=false;
      const refresh=()=>{
        preview.setAttribute('aria-label',t('imagePreview'));download.textContent=t('imageDownload');status.textContent=t(failure?'imageUnavailable':'imageLoading');
      };
      refresh();window.addEventListener('pot:locale',refresh);this.stopLocale=()=>window.removeEventListener('pot:locale',refresh);
      const failed=()=>{if(!this.isConnected||generation!==this.generation)return;failure=true;this.dataset.state='error';this.removeAttribute('aria-busy');image.hidden=true;preview.hidden=true;status.hidden=false;refresh();};
      const save=()=>Promise.resolve().then(()=>this.downloadFile(this.file)).catch(failed);
      download.onclick=save;
      preview.onclick=()=>{
        if(this.viewer)return;
        this.viewer=openImageViewer({image,label:this.label,trigger:preview,download:()=>this.downloadFile(this.file),onClose:()=>{this.viewer=null;}});
      };
      image.onload=async()=>{try{await image.decode();if(!this.isConnected||generation!==this.generation)return;this.dataset.state='ready';this.removeAttribute('aria-busy');status.hidden=true;image.hidden=false;preview.disabled=false;}catch{failed();}};image.onerror=failed;
      this.lease=this.acquireFileImage(this.file);
      this.lease.url.then(url=>{if(this.isConnected&&generation===this.generation)image.src=url;}).catch(failed);
    }
    disconnectedCallback() {
      ++this.generation;this.viewer?.close();this.stopLocale?.();this.lease?.release();this.lease=null;
    }
  });
}

export function createFileImage(file,label,{acquireFileImage,downloadFile}) {
  registerImage();const root=document.createElement('pot-file-image');
  Object.assign(root,{file,label:label||file.path.split('/').at(-1),acquireFileImage,downloadFile});return root;
}
