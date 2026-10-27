import {currentPreparedRender,preparedPreviousRoot} from './prepared-render.mjs';
import {moveConversationNode} from './conversation-dom.mjs';
import {createMessageFile} from './message-file.mjs';
import {agentFailureKey} from '../state/agent-links.mjs';
import {icon} from './icons.mjs';
import {createMarkdownView} from './markdown.mjs';
import {replyFailureKind} from '../state/chat-replies.mjs';
import {createResourceView} from './resource-view.mjs';

const node=(tag,className='',text)=>{const value=document.createElement(tag);value.className=className;if(text!==undefined)value.textContent=text;return value;};
const activePhases=new Set(['sending','thinking','streaming','awaiting','queued','cancelling','tools','approval','waiting','paused','reconnecting']);

// Keep the outer bubble and its content nodes alive while status changes. All
// failed replies use one presentation, including failures after partial output.
export function createReplyBubble(host) {
  const content=node('div','reply-content'),status=node('div','reply-status');
  status.setAttribute('aria-live','polite');
  host.classList.add('reply-bubble');host.append(content,status);
  const markdown=createMarkdownView(content,{slot:'reply'});
  const activityLine=node('div','reply-status-title'),activityIcon=node('span','reply-activity-icon'),activityLabel=node('span');
  const dots=node('span','waiting-dots'),dot=node('span','activity-dot'),clock=icon('clock');
  dots.setAttribute('aria-hidden','true');dots.append(node('i'),node('i'),node('i'));activityIcon.setAttribute('aria-hidden','true');activityIcon.append(dots,dot,clock);activityLine.append(activityIcon,activityLabel);
  let text,signature,resourceKey,resourceNode;const previewClaims=new Map();
  return {
    update(message,{messageFiles,retryMessageFile,t,downloadFile,acquireFileImage,acquireResource,downloadResource,onSettings,onRetrySend,canRetrySend}){
      const complete=message.phase==='complete'||message.status==='complete';
      const version=JSON.stringify([message.text,complete]);
      if(text!==version){const shown=new Set();markdown.update(message.text,{downloadFile,acquireFileImage,revision:complete,renderFileReference:messageFiles?reference=>{
        const normalized=reference.href.replace(/:[1-9][0-9]*$/,'');
        if(reference.block&&shown.has(normalized))reference={...reference,block:false};if(reference.block)shown.add(normalized);
        return createMessageFile(reference,message.id,{messageFiles,retryMessageFile,acquireResource,downloadResource,previewClaims,pending:!complete});
      }:undefined});text=version;}
      const nextResource=JSON.stringify(message.presentation||null);
      if(resourceKey!==nextResource){resourceNode?.remove();resourceNode=null;resourceKey=nextResource;if(message.presentation){resourceNode=createResourceView(message.presentation,{acquireResource,downloadResource});moveConversationNode(content,resourceNode);}}
      content.hidden=!message.text&&!message.presentation;
      const next=JSON.stringify([message.phase,message.error,t('replyFailed'),message.local,canRetrySend,message.activity]);
      if(signature===next)return;signature=next;
      const active=message.activity!==false&&activePhases.has(message.phase);
      host.classList.toggle('has-active-status',active);
      if(message.activity===false){status.hidden=true;host.classList.remove('is-waiting','is-streaming','is-failed');return;}
      if(active){
        status.removeAttribute('role');status.hidden=false;host.classList.remove('is-failed');host.classList.toggle('is-streaming',message.phase==='streaming');host.classList.toggle('is-waiting',message.phase!=='streaming');
        if(activityLine.parentNode!==status)status.replaceChildren(activityLine);
        dots.hidden=['approval','streaming','reconnecting','waiting','paused'].includes(message.phase);dot.hidden=message.phase!=='streaming';clock.toggleAttribute('hidden',!dots.hidden||!dot.hidden);
        activityLabel.textContent=t(({sending:'sending',streaming:'replyStreaming',tools:'tool.executing',approval:'tool.awaitingApproval',awaiting:'replyAwaiting',queued:'replyQueued',cancelling:'replyCancelling',waiting:'replyWaiting',paused:'replyPaused',reconnecting:'tool.reconnecting'})[message.phase]||'working');
        return;
      }
      const preparation=currentPreparedRender(),expanded=!!status.querySelector('details')?.open||preparation?.context.phase==='prepare'&&!!preparedPreviousRoot(preparation.context,preparation.member)?.querySelector('.reply-status details')?.open;
      status.replaceChildren();host.classList.toggle('is-failed',message.phase==='failed');
      host.classList.toggle('is-waiting',['sending','thinking','awaiting','queued','cancelling','tools','approval'].includes(message.phase));
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
        if(message.phase==='stopped')status.append(node('span','',t('replyStopped')));
        else if(message.phase==='unknown'){
          status.append(node('span','',t('replyUnknown')));
          if(message.error){const details=node('details','reply-error-details');details.open=expanded;details.append(node('summary','',t('errorDetails')),node('pre','',message.error));status.append(details);}
        }
      }
      status.hidden=!status.childElementCount;
    },
  };
}
