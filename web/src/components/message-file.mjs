import {translate} from '../locales/messages.mjs';
import {createResourceView} from './resource-view.mjs';

function register(){
  if(customElements.get('pot-message-file'))return;
  customElements.define('pot-message-file',class extends HTMLElement{
    connectedCallback(){
      if(this.started)return;this.started=true;this.abort=new AbortController();this.dataset.state='loading';
      this.link=document.createElement('a');this.link.href='#';this.link.textContent=this.reference.label||this.reference.href;this.link.onclick=event=>{event.preventDefault();this.open();};this.append(this.link);
      this.refresh=()=>{if(this.status)this.status.textContent=translate(document.documentElement.lang,`fileReference.${this.state||'pending'}`);if(this.retry)this.retry.textContent=translate(document.documentElement.lang,'retry');if(this.closeButton)this.closeButton.textContent=translate(document.documentElement.lang,'close');};
      window.addEventListener('pot:locale',this.refresh);this.load();
    }
    retryButton(){
      if(this.retry)return;this.retry=document.createElement('button');this.retry.type='button';this.retry.className='resource-action';
      this.retry.onclick=async()=>{this.retry.disabled=true;this.emptyReads=0;try{if(this.row)await this.options.retryMessageFile(this.message,this.row.id,{signal:this.abort.signal});await this.load();}catch{this.state='source_offline';this.refresh();}finally{if(this.retry)this.retry.disabled=false;}};this.append(this.retry);this.refresh();
    }
    async load(){
      try{
        const rows=await this.options.messageFiles(this.message,{signal:this.abort.signal});if(!this.isConnected)return;
        const row=rows.find(row=>row.href===this.reference.href);this.row=row;
        // Completion can reach the UI just before the separate binding commit.
        // Poll metadata briefly; an absent binding never authorizes a path read.
        if(!row&&(this.emptyReads=(this.emptyReads||0)+1)<5){this.state='pending';this.showStatus();this.timer=setTimeout(()=>this.load(),500);return;}
        this.state=row?.state||'unavailable';
        if(row?.state==='ready'&&row.resource){
          this.status?.remove();this.status=null;this.retry?.remove();this.retry=null;
          const claimed=this.options.previewClaims?.get(row.resource.id),canPreview=!claimed?.isConnected||claimed===this;
          if(this.reference.block&&!this.preview&&canPreview){this.options.previewClaims?.set(row.resource.id,this);this.link.hidden=true;this.preview=createResourceView({resource:row.resource,mode:'preview',caption:this.reference.label},{...this.options,noticeKey:'fileReference.savedVersion'});this.append(this.preview);}
          this.link.setAttribute('aria-label',this.reference.label||row.resource.name);this.dataset.state='ready';return;
        }
        this.showStatus();
        if(row&&['pending','resolving'].includes(row.state))this.timer=setTimeout(()=>this.load(),500);else{this.dataset.state='error';this.retryButton();}
      }catch(error){if(!this.abort.signal.aborted){this.dataset.state='error';this.state=error.status?'unavailable':'source_offline';this.showStatus();this.retryButton();}}
    }
    showStatus(){if(!this.status){this.status=document.createElement('span');this.status.className='file-reference-status';this.status.setAttribute('role','status');this.append(this.status);}this.refresh();}
    open(){
      if(!this.row?.resource)return;this.dialog?.close();
      const dialog=this.dialog=document.createElement('dialog');dialog.className='file-reference-dialog';dialog.setAttribute('aria-label',this.reference.label||this.row.resource.name);
      const close=this.closeButton=document.createElement('button');close.type='button';close.className='resource-action';close.textContent=translate(document.documentElement.lang,'close');close.onclick=()=>dialog.close();
      const line=Number(this.reference.href.match(/:([1-9][0-9]*)$/)?.[1]||0);
      dialog.append(close,createResourceView({resource:this.row.resource,mode:'preview',caption:this.reference.label},{...this.options,line,noticeKey:'fileReference.savedVersion'}));dialog.addEventListener('close',()=>{dialog.remove();if(this.isConnected)this.link.focus();},{once:true});document.body.append(dialog);dialog.showModal();
    }
    disconnectedCallback(){queueMicrotask(()=>{if(this.isConnected)return;clearTimeout(this.timer);this.abort?.abort();window.removeEventListener('pot:locale',this.refresh);this.dialog?.remove();if(this.row&&this.options.previewClaims?.get(this.row.resource?.id)===this)this.options.previewClaims.delete(this.row.resource.id);this.started=false;this.replaceChildren();this.preview=null;this.status=null;this.retry=null;});}
  });
}
export function createMessageFile(reference,message,options){register();const element=document.createElement('pot-message-file');element.dataset.block=String(!!reference.block);Object.assign(element,{reference,message,options,identity:JSON.stringify([message,reference.href,reference.block,reference.label])});return element;}
