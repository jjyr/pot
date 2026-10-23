// Selectors travel as ordinary message text, so drafts, receipts and history use
// the same representation. Assistant links carry the full node/agent identity;
// their display name is never used for routing and the links are not navigable.
const identifier='[A-Za-z0-9_-]{1,128}';
export const assistantMentionSource=String.raw`\[@((?:\\[\\\[\]]|[^\[\]\\\n]){1,240})\]\(pot-assistant:(${identifier})/(${identifier})\)`;
const assistantMentionPattern=new RegExp('^'+assistantMentionSource+'$','u');
export function parseAssistantMention(value){
  const match=String(value).match(assistantMentionPattern);
  return match?{label:match[1].replace(/\\([\\\[\]])/gu,'$1'),node:match[2],agent:match[3],token:'@assistant:'+match[2]+'/'+match[3]}:null;
}
export function selectorText(item){
  if(item.token.startsWith('/skill:'))return item.token;
  const match=item.token.match(new RegExp('^@assistant:('+identifier+')/('+identifier+')$','u'));
  if(!match)return '';
  const name=(item.label||match[2]).replace(/[\r\n]/gu,' ').replace(/[\\\[\]]/gu,'\\$&');
  return `[@${name}](pot-assistant:${match[1]}/${match[2]})`;
}
export function splitComposerValue(value){
  const newline=value.indexOf('\n'),head=newline<0?value:value.slice(0,newline),items=[],mentions=[];
  const pattern=new RegExp('^(?:/skill:[a-z0-9_-]{1,64}|'+assistantMentionSource+')(?: |$)','u');
  let rest=head;
  while(rest){const match=rest.match(pattern);if(!match)return {items:[],text:value};const raw=match[0].trimEnd(),mention=parseAssistantMention(raw);if(mention)mentions.push(raw);else items.push({token:raw,label:raw.slice(7),detail:'',raw});rest=rest.slice(match[0].length);}
  // Only Skills are extracted into chips. Keep existing mixed selector drafts'
  // mentions in the body without changing their stored text representation.
  const body=newline<0?'':value.slice(newline+1);
  return items.length?{items,text:mentions.join(' ')+(mentions.length&&body?'\n':'')+body}:{items:[],text:value};
}
export function joinComposerValue(items,text){return items.length?items.map(item=>item.raw||selectorText(item)).join(' ')+(text?'\n'+text:''):text;}
