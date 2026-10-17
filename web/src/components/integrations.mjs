import {node as n,action} from './primitives.mjs';

// The view only sees the integration catalog and login sessions. OAuth tokens
// and provider-specific polling remain behind the Integrations API.
export async function renderIntegrations({root,request,scope,signal,locale,openExternal}) {
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en,context={...scope};
  const section=n('section','integrations');root.append(section);
  section.append(n('h2','page-heading',tr('Third-party accounts','第三方账号')),n('p','hint',tr('Connect services for your assistants and tasks.','连接第三方服务，供助手和任务使用。')));
  const rows=n('div','integration-list');section.append(rows);
  let items=[],timer,busy=false;
  const active=()=>!signal.aborted;
  signal.addEventListener('abort',()=>clearTimeout(timer),{once:true});
  const call=(path,options={})=>request(path,{...context,signal,...options});
  const fail=(row,error)=>{if(!active())return;row.querySelector('.inline-error')?.remove();const message=n('p','inline-error',error.message||String(error));message.setAttribute('role','alert');row.append(message);};
  async function refresh(){if(!active())return;items=(await call('/api/v1/integrations')).items||[];if(active())render();}
  async function work(row,task){if(busy)return;busy=true;for(const control of rows.querySelectorAll('button'))control.disabled=true;try{await task();if(active())await refresh();}catch(error){fail(row,error);}finally{busy=false;if(active())for(const control of rows.querySelectorAll('button'))control.disabled=control.dataset.unavailable==='true';}}
  function render(){
    clearTimeout(timer);rows.replaceChildren();
    for(const item of items){
      const row=n('article','integration-row'),summary=n('div','integration-summary'),mark=n('span','integration-mark',item.name.slice(0,1)),info=n('div','row-info'),title=n('h3','integration-name',item.name),status=n('span','integration-status');
      const connected=!!item.account,pending=item.login?.status==='pending';
      status.textContent=item.status==='connected'?tr('Logged in','已登录'):item.status==='expired'?tr('Login expired','登录已失效'):pending?tr('Waiting for authorization','等待授权'):tr('Not logged in','未登录');status.dataset.status=item.status;status.setAttribute('role','status');info.append(title);if(connected)info.append(n('span','integration-identity','@'+item.account.identity.username));info.append(status);mark.setAttribute('aria-hidden','true');summary.append(mark,info);row.append(summary);
      const controls=n('div','integration-actions');row.append(controls);
      const base='/api/v1/integrations/'+encodeURIComponent(item.id);
      if(connected){controls.append(action(tr('Log out','退出登录'),()=>work(row,()=>call(base+'/connection',{method:'DELETE',body:{expected_revision:item.account.revision}}))));}
      else if(pending){
        controls.append(action(tr('Cancel','取消'),()=>work(row,()=>call(base+'/login/'+encodeURIComponent(item.login.id),{method:'DELETE'}))));
        if(item.login.authorization_url){const instructions=n('div','integration-authorization');instructions.append(n('p','hint',tr('Enter this code in your browser to authorize the connection.','在浏览器中输入此验证码，完成授权。')),n('code','integration-code',item.login.user_code));const link=n('a','control primary',tr('Open browser','打开浏览器'));link.href=item.login.authorization_url;link.target='_blank';link.rel='noopener noreferrer';if(openExternal)link.onclick=event=>{event.preventDefault();openExternal(link.href).catch(error=>fail(row,error));};instructions.append(link);row.append(instructions);}
      }else{
        const login=action(tr('Log in','登录'),()=>{
          // Reserve a browser tab inside the user gesture to avoid popup blockers.
          const popup=openExternal?null:window.open('about:blank','_blank');if(popup)popup.opener=null;
          return work(row,async()=>{try{const session=await call(base+'/login',{method:'POST',body:{}});if(!active()){popup?.close();return;}if(openExternal)await openExternal(session.authorization_url);else if(popup)popup.location.href=session.authorization_url;}catch(error){popup?.close();throw error;}});
        });login.disabled=!item.login_available;login.dataset.unavailable=String(!item.login_available);controls.append(login);
        if(!item.login_available)row.append(n('p','hint integration-note',tr('Login is not available on this Control. Contact its administrator.','此 Control 尚未启用登录，请联系管理员。')));
        if(item.login?.error)row.append(n('p','inline-error',tr('Authorization did not complete. Please try logging in again.','授权未完成，请重新登录。')));
      }
      rows.append(row);
    }
    if(items.some(item=>item.login?.status==='pending'))timer=setTimeout(()=>refresh().catch(error=>{if(active())fail(section,error);}),1500);
  }
  await refresh();
}
