import {translate} from '../locales/messages.mjs';

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
      this.replaceChildren(preview,status,download);this.dataset.state='loading';this.setAttribute('aria-busy','true');
      let failure=false;
      const refresh=()=>{
        preview.setAttribute('aria-label',t('imagePreview'));download.textContent=t('imageDownload');status.textContent=t(failure?'imageUnavailable':'imageLoading');
        if(this.dialog){this.dialog.querySelector('.file-image-close').setAttribute('aria-label',t('imageClose'));this.dialog.querySelector('.file-image-download').textContent=t('imageDownload');}
      };
      refresh();window.addEventListener('pot:locale',refresh);this.stopLocale=()=>window.removeEventListener('pot:locale',refresh);
      const failed=()=>{if(!this.isConnected||generation!==this.generation)return;failure=true;this.dataset.state='error';this.removeAttribute('aria-busy');image.hidden=true;preview.hidden=true;status.hidden=false;refresh();};
      const save=()=>Promise.resolve().then(()=>this.downloadFile(this.file)).catch(failed);
      download.onclick=save;
      preview.onclick=()=>{
        if(this.dialog?.open)return;
        const dialog=node('dialog','attachment-viewer'),header=node('div','attachment-viewer-toolbar'),title=node('span','attachment-name'),close=node('button','file-image-close control'),saveButton=node('button','file-image-download control'),full=node('img');
        close.type=saveButton.type='button';close.textContent='×';close.onclick=()=>dialog.close();saveButton.onclick=save;title.textContent=this.label;full.alt=this.label;full.src=image.src;
        header.append(title,saveButton,close);dialog.append(header,full);this.dialog=dialog;refresh();document.body.append(dialog);dialog.addEventListener('close',()=>{dialog.remove();if(this.dialog===dialog)this.dialog=null;},{once:true});dialog.showModal();
      };
      image.onload=async()=>{try{await image.decode();if(!this.isConnected||generation!==this.generation)return;this.dataset.state='ready';this.removeAttribute('aria-busy');status.hidden=true;image.hidden=false;preview.disabled=false;}catch{failed();}};image.onerror=failed;
      this.lease=this.acquireFileImage(this.file);
      this.lease.url.then(url=>{if(this.isConnected&&generation===this.generation)image.src=url;}).catch(failed);
    }
    disconnectedCallback() {
      ++this.generation;this.stopLocale?.();this.lease?.release();this.lease=null;this.dialog?.close();
    }
  });
}

export function createFileImage(file,label,{acquireFileImage,downloadFile}) {
  registerImage();const root=document.createElement('pot-file-image');
  Object.assign(root,{file,label:label||file.path.split('/').at(-1),acquireFileImage,downloadFile});return root;
}
