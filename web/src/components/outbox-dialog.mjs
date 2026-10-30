import {createMessageWindow} from './message-window.mjs';
import {preparedAsset} from './prepared-render.mjs';
import {moveConversationNode} from './conversation-dom.mjs';
import {updateMarkdown} from './markdown.mjs';
import {renderMessageAttachments} from './attachments.mjs';
import {renderMessageReferences} from './message-references.mjs';
import {outboxControls} from './outbox-controls.mjs';
import {copyFeedback} from './motion.mjs';
import {translate} from '../locales/messages.mjs';
import {outboxIdentityKey} from '../state/chat-outbox-store.mjs';
const node=(tag,className,text)=>{const value=document.createElement(tag);value.className=className;if(text!==undefined)value.textContent=text;return value;};

// Local inspection survives server-side deletion and identity retirement. It is
// not an assistant/session and never changes the active destination or draft.
export function createOutboxDialog({client,outbox,locale,notify}){
  const t=key=>translate(locale(),key),root=node('dialog','outbox-dialog'),heading=node('div','outbox-heading'),title=node('h2'),close=node('button','text-button'),history=node('div','history'),host=node('div','message-window'),empty=node('p','hint'),notice=node('p','inline-error'),editors=new Map();
  close.type='button';close.onclick=()=>root.close();heading.append(title,close);history.append(host);notice.setAttribute('role','alert');root.append(heading,notice,empty,history);
  const report=value=>{notice.textContent=value;notify(value);};
  let closed=false,savedRows=[],generation=0;
  const render=()=>{
    if(closed)return;title.textContent=t('outbox.title');close.textContent=t('close');root.setAttribute('aria-label',t('outbox.title'));empty.textContent=t('outbox.empty');
    if(!root.open)return;
    const rows=savedRows.filter(value=>value.state!=='cancelled');empty.hidden=!!rows.length;
    messageWindow.setMessages(rows.map(value=>{const key=outboxIdentityKey(value),id='saved:'+encodeURIComponent(key);return {id,viewKey:id,role:'user',text:value.request.text,queue:value,editing:editors.has(key)};}),{layoutEpoch:[locale()],renderContext:{}});
  };
  const messageWindow=createMessageWindow({scroller:history,host,active:()=>root.open&&!closed,requestRender:()=>render(),render(message,existing){
    const value=message.queue,scope={space:value.target.space,owner:value.target.owner};
    const record=existing||{row:node('article','message role-user')};
    const signature=JSON.stringify([outboxIdentityKey(value),value.revision,locale(),editors.has(outboxIdentityKey(value))]);
    const body=preparedAsset('saved-message',signature,()=>{
      const body=node('div','message-body'),destination=node('p','hint',value.destination?.name||t('outbox.destination'));
      if(value.destination?.space)destination.textContent+=' · '+value.destination.space;
      body.append(destination);const prose=node('div','message-prose');updateMarkdown(prose,value.request.text,{renderSlot:'saved-message-prose'});body.append(prose);
      if(value.request.references?.length)body.append(renderMessageReferences(value.request.references,{messages:[],locale}));
      if(value.request.attachments?.length)body.append(renderMessageAttachments(value.request.attachments,{client,scope,locale,originalResource:attachment=>outbox.acquireOriginal(value.target.key,value.request.request_id,attachment,scope)}));
      body.append(outboxControls(value,{outbox,editors,locale,changed:()=>render(),notify:report}));
      const copy=node('button','text-button',t('copy'));copy.type='button';copyFeedback(copy,{text:()=>value.request.text,locale,translate,onError:()=>report(t('tool.copyFailed'))});body.append(copy);
      return body;
    });
    if(record.body!==body){record.body?.remove();record.body=body;moveConversationNode(record.row,body);}
    return record;
  },dispose:record=>{record.body?.querySelectorAll('.message-attachments').forEach(node=>node.dispose?.());record.row.remove();}});
  async function update(){
    if(closed)return;
    render();
    if(!root.open)return;
    const current=++generation;
    try{
      const rows=await outbox.saved();
      if(closed||current!==generation)return;
      savedRows=rows;
      render();
    }catch(error){
      if(closed||current!==generation)return;
      report(t('outbox.storageFailed'));
    }
  }
  root.addEventListener('close',()=>{generation++;messageWindow.suspend();});
  return {root,update,open(){if(closed)return;root.showModal();update();},close(){generation++;closed=true;messageWindow.close();root.close();root.remove();}};
}
