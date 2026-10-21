import {agentFailureKey} from '../state/agent-links.mjs';
import {icon} from './icons.mjs';
import {createMarkdownView} from './markdown.mjs';
import {replyFailureKind} from '../state/chat-replies.mjs';
import {createResourceView} from './resource-view.mjs';

const node=(tag,className='',text)=>{const value=document.createElement(tag);value.className=className;if(text!==undefined)value.textContent=text;return value;};

// Keep the outer bubble and its content nodes alive while status changes. All
// failed replies use one presentation, including failures after partial output.
export function createReplyBubble(host) {
  const content=node('div','reply-content'),status=node('div','reply-status');
  status.setAttribute('aria-live','polite');
  host.classList.add('reply-bubble');host.append(content,status);
  const markdown=createMarkdownView(content);
  let text,signature,resourceKey,resourceNode;
  return {
    update(message,{t,downloadFile,acquireFileImage,acquireResource,downloadResource,onSettings,onReconnect,onRetrySend,canRetrySend}){
      if(text!==message.text){markdown.update(message.text,{downloadFile,acquireFileImage});text=message.text;}
      const nextResource=JSON.stringify(message.presentation||null);
      if(resourceKey!==nextResource){resourceNode?.remove();resourceNode=null;resourceKey=nextResource;if(message.presentation){resourceNode=createResourceView(message.presentation,{acquireResource,downloadResource});content.append(resourceNode);}}
      content.hidden=!message.text&&!message.presentation;
      const next=JSON.stringify([message.phase,message.error,t('replyFailed'),message.local,canRetrySend]);
      if(signature===next)return;signature=next;
      const expanded=!!status.querySelector('details')?.open;
      status.replaceChildren();host.classList.toggle('is-failed',message.phase==='failed');
      host.classList.toggle('is-waiting',['sending','thinking','awaiting','queued','cancelling','reconnecting','tools','approval'].includes(message.phase));
      host.classList.toggle('is-streaming',message.phase==='streaming');
      const action=(label,run)=>{const button=node('button','reply-action control',label);button.type='button';button.onclick=run;return button;};
      if(message.phase==='failed'){
        status.setAttribute('role','alert');
        const title=node('div','reply-status-title');title.append(icon('alert'),node('span','',t('replyFailed')));status.append(title);
        const kind=replyFailureKind(message.error);
        status.append(node('p','reply-error-summary',t(agentFailureKey(message.error)||`replyError.${kind}`)));
        if(message.error){const details=node('details','reply-error-details');details.open=expanded;details.append(node('summary','',t('errorDetails')),node('pre','',message.error));status.append(details);}
        if(message.local){const retry=action(t('retry'),onRetrySend);retry.disabled=!canRetrySend;status.append(retry);}
        else if(kind==='configuration'&&onSettings)status.append(action(t('checkAISettings'),onSettings));
      }else{
        status.removeAttribute('role');
        if(['sending','thinking','awaiting','queued','cancelling','reconnecting','tools','approval'].includes(message.phase)){
          const line=node('div','reply-status-title');
          if(!['reconnecting','approval'].includes(message.phase)){const dots=node('span','waiting-dots');dots.setAttribute('aria-hidden','true');dots.append(node('i'),node('i'),node('i'));line.append(dots);}
          else line.append(icon(message.phase==='approval'?'clock':'cloud'));
          line.append(node('span','',t(({sending:'sending',reconnecting:'replyReconnecting',tools:'tool.executing',approval:'tool.awaitingApproval',awaiting:'replyAwaiting',queued:'replyQueued',cancelling:'replyCancelling'})[message.phase]||'working')));status.append(line);
          if(message.phase==='reconnecting'){status.append(node('p','reply-connection-detail',t('replyStateUnknown')),...(onReconnect?[action(t('reconnect'),onReconnect)]:[]));}
        }else if(message.phase==='streaming'){const dot=node('span','activity-dot');dot.setAttribute('aria-label',t('working'));status.append(dot);}
        else if(message.phase==='stopped')status.append(node('span','',t('replyStopped')));
        else if(message.phase==='paused')status.append(node('span','',t('replyPaused')));
        else if(message.phase==='waiting')status.append(node('span','',t('replyWaiting')));
        else if(message.phase==='empty')status.append(node('span','',t('replyEmpty')));
        else if(message.phase==='unknown'){
          status.append(node('span','',t('replyUnknown')));
          if(message.error){const details=node('details','reply-error-details');details.open=expanded;details.append(node('summary','',t('errorDetails')),node('pre','',message.error));status.append(details);}
        }
      }
      status.hidden=!status.childElementCount;
    },
  };
}
