import {node as n,field,action} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

export function notificationSetting({notifications,locale,desktop,host=window}) {
  const t=key=>translate(locale(),key);
  const root=n('section','notification-setting');
  const toggle=field(t('notifications.setting'),true,{kind:'checkbox'});
  toggle.row.classList.add('preference-setting');
  const status=n('p','hint');status.setAttribute('role','status');
  const details=n('details'),summary=n('summary'),diagnostic=n('pre');details.append(summary,diagnostic);details.hidden=true;
  let actionError='',pending=false;
  const request=action(t('notifications.allow'),async()=>{
    pending=true;actionError='';update();
    try{await notifications.authorize();}catch(reason){actionError=reason.message||String(reason);}finally{pending=false;update();}
  });
  const note=n('p','hint');root.append(toggle.row,status,request,note,details);
  function update(){
    const {enabled,permission,error}=notifications.state;toggle.input.checked=enabled;
    toggle.row.querySelector('.form-label').textContent=t('notifications.setting');
    toggle.input.setAttribute('aria-label',t('notifications.setting'));
    request.textContent=t('notifications.allow');request.disabled=pending||notifications.state.authorizing;
    const key=permission==='denied'?(desktop?'deniedMac':'deniedBrowser'):permission;
    root.setAttribute('aria-busy',String(permission==='loading'));status.hidden=enabled&&permission==='loading'&&!error&&!actionError;status.textContent=[enabled&&permission==='loading'?'':t(!enabled?'notifications.off':`notifications.${key}`),(error||actionError)?t('notifications.failed'):''].filter(Boolean).join(' ');
    request.hidden=!enabled||permission!=='default'&&!notifications.state.needsSubscription;
    note.textContent=[t('notifications.device'),notifications.state.channel?t(`notifications.channel.${notifications.state.channel}`):''].filter(Boolean).join(' ');summary.textContent=t('notifications.details');
    details.hidden=!(error||actionError);diagnostic.textContent=error||actionError;
  }
  toggle.input.onchange=async()=>{
    toggle.input.disabled=true;actionError='';
    try{await notifications.setEnabled(toggle.input.checked);}catch(reason){actionError=reason.message||String(reason);}finally{toggle.input.disabled=false;update();}
  };
  const stop=notifications.subscribe(update);host.addEventListener('pot:locale',update);update();
  return {element:root,dispose(){stop();host.removeEventListener('pot:locale',update);}};
}
