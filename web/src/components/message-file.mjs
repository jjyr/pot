import {preparedIsland,deferPreparedConnection} from './prepared-render.mjs';
import {translate} from '../locales/messages.mjs';
import {createResourceView} from './resource-view.mjs';
import {lockDialogScroll} from './dialog-scroll.mjs';

function register(){
  if(customElements.get('pot-message-file'))return;
  customElements.define('pot-message-file',class extends HTMLElement{
    connectedCallback(){
      if(this.preparationSurrogate||this.started)return;if(deferPreparedConnection(this,()=>this.connectedCallback()))return;this.started=true;this.generation=(this.generation||0)+1;this.abort=new AbortController();this.dataset.state='loading';this.setAttribute('aria-busy','true');
      this.link=document.createElement('a');this.link.href='#';this.link.textContent=this.reference.label||this.reference.href;this.link.onclick=event=>{event.preventDefault();this.open();};this.append(this.link);
      this.refresh=()=>{if(this.status)this.status.textContent=translate(document.documentElement.lang,`fileReference.${this.state||'pending'}`);if(this.retry)this.retry.textContent=translate(document.documentElement.lang,'retry');if(this.closeButton)this.closeButton.textContent=translate(document.documentElement.lang,'close');};
      window.addEventListener('pot:locale',this.refresh);this.load();
    }
    retryButton(){
      if(this.retry)return;this.retry=document.createElement('button');this.retry.type='button';this.retry.className='resource-action';
      const generation=this.generation,abort=this.abort,current=()=>this.started&&generation===this.generation&&!abort.signal.aborted;
      this.retry.onclick=async()=>{this.retry.disabled=true;this.emptyReads=0;try{if(this.row)await this.options.retryMessageFile(this.message,this.row.id,{signal:abort.signal});if(current())await this.load();}catch{if(current()){this.state='source_offline';this.refresh();}}finally{if(current()&&this.retry)this.retry.disabled=false;}};this.append(this.retry);this.refresh();
    }
    async load(){
      const read=this.readGeneration=(this.readGeneration||0)+1;
      const generation=this.generation,abort=this.abort,current=()=>this.isConnected&&this.started&&generation===this.generation&&read===this.readGeneration&&!abort.signal.aborted;
      try{
        const rows=await this.options.messageFiles(this.message,{signal:abort.signal,sourceText:this.options.sourceText});if(!current())return;
        const row=rows.find(row=>row.href===this.reference.href);this.row=row;
        // Completion can reach the UI just before the separate binding commit.
        // Poll metadata briefly; an absent binding never authorizes a path read.
        if(!row&&((this.emptyReads=(this.emptyReads||0)+1)<5||this.options.pending)){this.state='pending';this.showStatus();this.timer=setTimeout(()=>this.load(),500);return;}
        this.state=row?.state||'unavailable';
        if(row?.state==='ready'&&row.resource){
          this.status?.remove();this.status=null;this.retry?.remove();this.retry=null;
          const claimed=this.options.previewClaims?.get(row.resource.id),canPreview=!claimed?.isConnected||claimed===this;
          if(this.reference.block&&!this.preview&&canPreview){this.options.previewClaims?.set(row.resource.id,this);this.link.hidden=true;this.preview=createResourceView({resource:row.resource,mode:'preview',caption:this.reference.label},{...this.options});this.append(this.preview);}
          this.link.setAttribute('aria-label',this.reference.label||row.resource.name);this.dataset.state='ready';this.removeAttribute('aria-busy');return;
        }
        this.showStatus();
        if(row&&['pending','resolving'].includes(row.state))this.timer=setTimeout(()=>this.load(),500);else{this.dataset.state='error';this.removeAttribute('aria-busy');this.retryButton();}
      }catch(error){if(current()){this.dataset.state='error';this.removeAttribute('aria-busy');this.state=error.status?'unavailable':'source_offline';this.showStatus();this.retryButton();}}
    }
    showStatus(){if(['pending','resolving'].includes(this.state)){this.status?.remove();this.status=null;return;}if(!this.status){this.status=document.createElement('span');this.status.className='file-reference-status';this.status.setAttribute('role','status');this.append(this.status);}this.refresh();}
    updatePreparedProperties(properties){const pending=this.options.pending,sourceText=this.options.sourceText;Object.assign(this,properties);if((pending&&!this.options.pending||sourceText!==this.options.sourceText)&&this.started&&!this.preview){clearTimeout(this.timer);this.emptyReads=0;this.load();}}
    open(){
      if(!this.row?.resource)return;this.dialog?.close();this.releaseDialogScroll?.();
      const dialog=this.dialog=document.createElement('dialog');dialog.className='file-reference-dialog';dialog.setAttribute('aria-label',this.reference.label||this.row.resource.name);
      const close=this.closeButton=document.createElement('button');close.type='button';close.className='resource-action';close.textContent=translate(document.documentElement.lang,'close');close.onclick=()=>dialog.close();
      const line=Number(this.reference.href.match(/:([1-9][0-9]*)$/)?.[1]||0);
      const release=this.releaseDialogScroll=lockDialogScroll(this.link);
      dialog.append(close,createResourceView({resource:this.row.resource,mode:'preview',caption:this.reference.label},{...this.options,line}));dialog.addEventListener('close',()=>{release();dialog.remove();if(this.dialog===dialog){this.dialog=null;this.releaseDialogScroll=null;if(this.isConnected)this.link.focus({preventScroll:true});}},{once:true});document.body.append(dialog);dialog.showModal();
    }
    disconnectedCallback(){queueMicrotask(()=>{if(!this.isConnected)this.retireRendering();});}
    retireRendering(){this.preparationTicket?.release();if(!this.started)return;this.started=false;++this.generation;clearTimeout(this.timer);this.abort?.abort();window.removeEventListener('pot:locale',this.refresh);this.releaseDialogScroll?.();this.releaseDialogScroll=null;this.dialog?.remove();this.dialog=null;if(this.row&&this.options.previewClaims?.get(this.row.resource?.id)===this)this.options.previewClaims.delete(this.row.resource.id);this.started=false;this.replaceChildren();this.preview=null;this.status=null;this.retry=null;}
  });
}
export function createMessageFile(reference,message,options){register();const element=document.createElement('pot-message-file');element.dataset.block=String(!!reference.block);Object.assign(element,{reference,message,options,identity:JSON.stringify([message,reference.href,reference.block,reference.label])});return preparedIsland(element);}
