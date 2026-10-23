// Shared literal DOM rendering for @assistant identities (including display names).
export function assistantMention({label,node,agent}){
  const mention=document.createElement('span');mention.className='assistant-mention';
  mention.textContent='@'+label;mention.title=node+' · '+agent;return mention;
}
