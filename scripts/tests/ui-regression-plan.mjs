// Behavior groups shared by selection, listing and the runner contract.
export const groups = {
  scroll: ['controller', 'continuity', 'task-continuity', 'scroll-components'],
  nav: ['directory', 'switching'],
  history: ['task-history', 'task-history-edges'],
  domain: ['domain-repositories.html'],
  management: ['assistant-start-phone','first-connection-codex','first-connection-antigravity','settings-phone','machines'],
  composer: ['composer-phone','composer-enter','reference-cancel'],
  connection: ['chat-stream-recovery'],
  approvals: ['linked-approvals','patch-approvals'],
  usage: ['usage-empty','usage-unknown','usage-partial','usage-trend'],
  storage: ['chat-cache-read-volume.html', 'chat-cache.html', 'projection-store.html', 'chat-cache-ui.html', 'cached-loading.html'],
};
export const fixtures = {
  'domain-repositories.html': 'DONE conversation actions authority contract',
  'chat-cache-read-volume.html': 'PASS bounded chat reads',
  'chat-cache.html': 'DONE cache contract',
  'projection-store.html': 'DONE projection store contract',
  'chat-cache-ui.html': 'DONE',
  'cached-loading.html': 'ALL CHECKS PASSED',
};
export const faults = {
  'controller-ownership': {group:'scroll', path:'/web/src/components/conversation-controller.mjs', from:'virtualizer.shouldAdjustScrollPositionOnItemSizeChange=()=>false;', to:''},
  'scroll-flow': {group:'scroll', path:'/web/src/components/message-window.mjs', from:'host.style.minHeight=`${Math.max(host.getBoundingClientRect().height,virtualizer.getTotalSize())}px`;', to:''},
  'directory-grid': {group:'nav', path:'/web/src/styles.css', from:'position:absolute;grid-area:auto;top:calc(var(--titlebar-height)', to:'position:absolute;top:calc(var(--titlebar-height)'},
  'history-cursor': {group:'history', path:'/web/src/components/task-conversation.mjs', from:'historyAttempt===key||disposed', to:'disposed'},
  'domain-progress': {group:'domain', path:'/web/src/state/domains/tasks.mjs', from:"onProgress:pages=>publish({data:{pages,partial:true},status:'loading',freshness:'retained'})", to:'onProgress:()=>{}'},
};
export function select(group) {
  if(group === 'all')return Object.values(groups).flat();
  if(!Object.hasOwn(groups,group))throw Error('Unknown group: '+group);
  return groups[group];
}
export function injectFault(path, source, fault) {
  const spec=faults[fault];
  if(!spec || spec.path!==path)return source;
  if(source.split(spec.from).length!==2)throw Error('Fault no longer matches exactly once: '+fault);
  return source.replace(spec.from,spec.to);
}
