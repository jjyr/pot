import {node as n,action} from './primitives.mjs';
import {icon} from './icons.mjs';
import {translate} from '../locales/messages.mjs';
import {createPluginNotifications} from './plugin-notifications.mjs';
import {reconcile} from './reconcile.mjs';

// The view only sees the integration catalog and login sessions. OAuth tokens
// and provider-specific polling remain behind the Integrations API.
export function createIntegrations({root,request,scope,signal,locale,openExternal,refresh}) {
  const t=key=>translate(locale(),key),context={...scope};
  const section=n('section','integrations');root.append(section);
  const heading=n('h2','page-heading'),description=n('p','hint');section.append(heading,description);
  const rows=n('div','integration-list');section.append(rows);
  const notifications=createPluginNotifications({pluginID: 'github',request,scope:context,signal,locale,refresh,render});
  let expanded=false,accountIdentity=null;
  let items=[],timer,busy=false;
  const active=()=>!signal.aborted;
  window.addEventListener('pot:locale',render);
  signal.addEventListener('abort',()=>{clearTimeout(timer);window.removeEventListener('pot:locale',render);},{once:true});
  const call=(path,options={})=>request(path,{...context,signal,...options});
  const fail=(row,error)=>{if(!active())return;row.querySelector('.inline-error')?.remove();const message=n('p','inline-error',error.message||String(error));message.setAttribute('role','alert');row.append(message);};
  async function work(button,task){const row=button.closest('.integration-row');if(busy)return;busy=true;for(const control of rows.querySelectorAll('.integration-actions button,.integration-toggle'))control.disabled=true;try{await task();if(active())await refresh();}catch(error){fail(row,error);}finally{busy=false;if(active())for(const control of rows.querySelectorAll('.integration-actions button,.integration-toggle'))control.disabled=control.dataset.unavailable==='true';}}
  function render(){
    heading.textContent=t('integrations.title');description.textContent=t('integrations.description');
    clearTimeout(timer);const projection=n('div','integration-list');
    for(const item of items){
      const connected=!!item.account,pending=item.login?.status==='pending',github=connected&&item.id==='github';
      const row=n('article','integration-row'),summary=n(github?'button':'div','integration-summary'),mark=n('span','integration-mark',item.name.slice(0,1)),info=n('div','row-info'),title=n('h3','integration-name',item.name),status=n('span','integration-status');
      if(item.id==='github')mark.replaceChildren(icon('github'));
      row.dataset.viewKey=item.id;
      status.textContent=item.status==='connected'?t('integrations.status.connected'):item.status==='expired'?t('integrations.status.expired'):pending?t('integrations.status.pending'):t('integrations.status.disconnected');status.dataset.status=item.status;status.setAttribute('role','status');info.append(title);const connection=n('div','integration-connection');if(connected)connection.append(n('span','integration-identity','@'+item.account.identity.username));connection.append(status);info.append(connection);mark.setAttribute('aria-hidden','true');summary.append(mark,info);row.append(summary);
      if(github){
        row.classList.add('github-account');summary.type='button';summary.classList.add('integration-toggle');summary.setAttribute('aria-expanded',String(expanded));summary.setAttribute('aria-controls','github-account-settings');
        summary.onclick=()=>{expanded=!expanded;render();};
        const notification=notifications.summary(),label=n('span','integration-notification',notification.text);label.dataset.error=String(!!notification.error);label.setAttribute('role','status');info.append(label);summary.setAttribute('aria-label',t('integrations.accountSummary').replace('{username}',item.account.identity.username).replace('{status}',status.textContent).replace('{notifications}',notification.text));
        const chevron=n('span','integration-chevron',expanded?'⌃':'⌄');chevron.setAttribute('aria-hidden','true');summary.append(chevron);
      }
      const controls=n('div','integration-actions');
      if(github){const panel=n('div','github-account-settings');panel.id='github-account-settings';panel.hidden=!expanded;panel.append(notifications.form(),controls);row.append(panel);}else row.append(controls);
      const base='/api/v1/integrations/'+encodeURIComponent(item.id);
      if(connected){controls.append(action(t('integrations.logout'),event=>work(event.currentTarget,()=>call(base+'/connection',{method:'DELETE',body:{expected_revision:item.account.revision}}))));}
      else if(pending){
        controls.append(action(t('integrations.cancel'),event=>work(event.currentTarget,()=>call(base+'/login/'+encodeURIComponent(item.login.id),{method:'DELETE'}))));
        if(item.login.authorization_url){const instructions=n('div','integration-authorization');instructions.append(n('p','hint',t('integrations.authorization.instructions')),n('code','integration-code',item.login.user_code));const link=n('a','control primary',t('integrations.authorization.openBrowser'));link.href=item.login.authorization_url;link.target='_blank';link.rel='noopener noreferrer';if(openExternal)link.onclick=event=>{event.preventDefault();const link=event.currentTarget;openExternal(link.href).catch(error=>fail(link.closest('.integration-row'),error));};instructions.append(link);row.append(instructions);}
      }else{
        const login=action(t('integrations.login'),event=>{const button=event.currentTarget;
          // Reserve a browser tab inside the user gesture to avoid popup blockers.
          const popup=openExternal?null:window.open('about:blank','_blank');if(popup)popup.opener=null;
          return work(button,async()=>{try{const session=await call(base+'/login',{method:'POST',body:{}});if(!active()){popup?.close();return;}if(openExternal)await openExternal(session.authorization_url);else if(popup)popup.location.href=session.authorization_url;}catch(error){popup?.close();throw error;}});
        });login.disabled=!item.login_available;login.dataset.unavailable=String(!item.login_available);controls.append(login);
        if(!item.login_available)row.append(n('p','hint integration-note',t('integrations.loginUnavailable')));
        if(item.login?.error)row.append(n('p','inline-error',t('integrations.authorization.failed')));
      }
      for(const control of controls.querySelectorAll('button')){control.dataset.unavailable=String(control.disabled);control.disabled=busy||control.disabled;}projection.append(row);
    }
    reconcile(rows,projection);
    if(items.some(item=>item.login?.status==='pending'))timer=setTimeout(()=>refresh().catch(error=>{if(active())fail(section,error);}),1500);
  }
  return value=>{items=value.items||[];const account=items.find(item=>item.id==='github')?.account;const identity=account?.identity.id||null;if(accountIdentity!==identity){expanded=false;accountIdentity=identity;}notifications.update(value.notifications,account);render();};
}
