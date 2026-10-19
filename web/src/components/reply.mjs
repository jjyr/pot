import {icon} from './icons.mjs';
import {renderMarkdown} from './markdown.mjs';
import {replyFailureKind} from '../state/chat-replies.mjs';

const node=(tag,className='',text)=>{const value=document.createElement(tag);value.className=className;if(text!==undefined)value.textContent=text;return value;};

// Keep the outer bubble and its content nodes alive while status changes. All
// failed replies use one presentation, including failures after partial output.
export function createReplyBubble(host) {
  const content=node('div','reply-content'),status=node('div','reply-status');
  status.setAttribute('aria-live','polite');
  host.classList.add('reply-bubble');host.append(content,status);
  let text,signature;
  return {
    update(message,{t,downloadFile,acquireFileImage,onSettings,onReconnect,onRetrySend,canRetrySend}){
      if(text!==message.text){content.replaceChildren();if(message.text)content.append(renderMarkdown(message.text,{downloadFile,acquireFileImage}));text=message.text;}
      content.hidden=!message.text;
      const next=JSON.stringify([message.phase,message.error,t('replyFailed'),message.local,canRetrySend]);
      if(signature===next)return;signature=next;
      const expanded=!!status.querySelector('details')?.open;
      status.replaceChildren();host.classList.toggle('is-failed',message.phase==='failed');
      host.classList.toggle('is-waiting',['sending','thinking','reconnecting','tools','approval'].includes(message.phase));
      host.classList.toggle('is-streaming',message.phase==='streaming');
      const action=(label,run)=>{const button=node('button','reply-action control',label);button.type='button';button.onclick=run;return button;};
      if(message.phase==='failed'){
        status.setAttribute('role','alert');
        const title=node('div','reply-status-title');title.append(icon('alert'),node('span','',t('replyFailed')));status.append(title);
        const kind=replyFailureKind(message.error);
        status.append(node('p','reply-error-summary',t(`replyError.${kind}`)));
        if(message.error){const details=node('details','reply-error-details');details.open=expanded;details.append(node('summary','',t('errorDetails')),node('pre','',message.error));status.append(details);}
        if(message.local){const retry=action(t('retry'),onRetrySend);retry.disabled=!canRetrySend;status.append(retry);}
        else if(kind==='configuration')status.append(action(t('checkAISettings'),onSettings));
      }else{
        status.removeAttribute('role');
        if(['sending','thinking','reconnecting','tools','approval'].includes(message.phase)){
          const line=node('div','reply-status-title');
          if(!['reconnecting','approval'].includes(message.phase)){const dots=node('span','waiting-dots');dots.setAttribute('aria-hidden','true');dots.append(node('i'),node('i'),node('i'));line.append(dots);}
          else line.append(icon(message.phase==='approval'?'clock':'cloud'));
          line.append(node('span','',t(message.phase==='sending'?'sending':message.phase==='reconnecting'?'replyReconnecting':message.phase==='tools'?'tool.executing':message.phase==='approval'?'tool.awaitingApproval':'working')));status.append(line);
          if(message.phase==='reconnecting'){status.append(node('p','reply-connection-detail',t('replyStateUnknown')),action(t('reconnect'),onReconnect));}
        }else if(message.phase==='streaming'){const dot=node('span','activity-dot');dot.setAttribute('aria-label',t('working'));status.append(dot);}
        else if(message.phase==='stopped')status.append(node('span','',t('replyStopped')));
        else if(message.phase==='paused')status.append(node('span','',t('replyPaused')));
        else if(message.phase==='waiting')status.append(node('span','',t('replyWaiting')));
      }
      status.hidden=!status.childElementCount;
    },
  };
}
