import {node as n,action} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';

// Mounted once beside the shell, so navigation/locale changes retain the same
// controls and pending permission request. This card never takes keyboard focus.
export function notificationOnboarding({notifications,locale,host=window}) {
  const t=key=>translate(locale(),key),root=n('section','notification-onboarding');
  root.hidden=true;root.setAttribute('aria-labelledby','notification-onboarding-title');
  const copy=n('div'),title=n('strong'),body=n('p','hint');title.id='notification-onboarding-title';copy.append(title,body);
  const controls=n('div','notification-onboarding-actions');
  const allow=action('',()=>{notifications.authorize().catch(()=>{});},{primary:true});
  const later=action('',()=>notifications.dismissOnboarding());controls.append(allow,later);root.append(copy,controls);
  function update(){
    root.hidden=!notifications.state.onboarding;
    title.textContent=t('notifications.onboarding.title');body.textContent=t('notifications.onboarding.body');
    allow.textContent=t('notifications.allow');later.textContent=t('notifications.later');
  }
  const stop=notifications.subscribe(update);host.addEventListener('pot:locale',update);update();
  return {element:root,dispose(){stop();host.removeEventListener('pot:locale',update);}};
}
