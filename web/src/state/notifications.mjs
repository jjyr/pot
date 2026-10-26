import {translate} from '../locales/messages.mjs';
// Presentation only. Event selection, identity, preview bounds and foreground
// policy belong to the shared backend systemnotifications service.
export function notificationText(event,locale='en'){
  return {title:`Pot · ${event.name||'Pot'}`,body:(event.kind==='reply'&&event.preview)||translate(locale,event.body_key)};
}
