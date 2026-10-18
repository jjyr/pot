import {node as n,action} from './primitives.mjs';
import {icon} from './icons.mjs';
import {translate} from '../locales/messages.mjs';
import {reconcile} from './reconcile.mjs';

// The view only sees the integration catalog and login sessions. OAuth tokens
// and provider-specific polling remain behind the Integrations API.
export function createIntegrations({root,request,scope,signal,locale,openExternal,refresh}) {
  const t=key=>translate(locale(),key),context={...scope};
  const section=n('section','integrations');root.append(section);
  section.append(n('h2','page-heading',t('integrations.title')),n('p','hint',t('integrations.description')));
  const rows=n('div','integration-list');section.append(rows);
  let items=[],timer,busy=false;
  const active=()=>!signal.aborted;
  signal.addEventListener('abort',()=>clearTimeout(timer),{once:true});
  const call=(path,options={})=>request(path,{...context,signal,...options});
  const fail=(row,error)=>{if(!active())return;row.querySelector('.inline-error')?.remove();const message=n('p','inline-error',error.message||String(error));message.setAttribute('role','alert');row.append(message);};
  async function work(button,task){const row=button.closest('.integration-row');if(busy)return;busy=true;for(const control of rows.querySelectorAll('button'))control.disabled=true;try{await task();if(active())await refresh();}catch(error){fail(row,error);}finally{busy=false;if(active())for(const control of rows.querySelectorAll('button'))control.disabled=control.dataset.unavailable==='true';}}
  function render(){
    clearTimeout(timer);const projection=n('div','integration-list');
    for(const item of items){
      const row=n('article','integration-row'),summary=n('div','integration-summary'),mark=n('span','integration-mark',item.name.slice(0,1)),info=n('div','row-info'),title=n('h3','integration-name',item.name),status=n('span','integration-status');
      if(item.id==='github')mark.replaceChildren(icon('github'));
      row.dataset.viewKey=item.id;
      const connected=!!item.account,pending=item.login?.status==='pending';
      status.textContent=item.status==='connected'?t('integrations.status.connected'):item.status==='expired'?t('integrations.status.expired'):pending?t('integrations.status.pending'):t('integrations.status.disconnected');status.dataset.status=item.status;status.setAttribute('role','status');info.append(title);if(connected)info.append(n('span','integration-identity','@'+item.account.identity.username));info.append(status);mark.setAttribute('aria-hidden','true');summary.append(mark,info);row.append(summary);
      const controls=n('div','integration-actions');row.append(controls);
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
      for(const control of row.querySelectorAll('button')){control.dataset.unavailable=String(control.disabled);control.disabled=busy||control.disabled;}projection.append(row);
    }
    reconcile(rows,projection);
    if(items.some(item=>item.login?.status==='pending'))timer=setTimeout(()=>refresh().catch(error=>{if(active())fail(section,error);}),1500);
  }
  return value=>{items=value.items||[];render();};
}
