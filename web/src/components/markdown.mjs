import {currentPreparedRender,preparedAsset,staticPreparationClone,preparedPreviousRoot} from './prepared-render.mjs';
import {moveConversationNode} from './conversation-dom.mjs';
import {inlineLinks,fileHref} from '../state/markdown-links.mjs';
import {createMermaidBlock} from './mermaid.mjs';
import {createFileImage} from './file-image.mjs';
import {assistantMentionSource,parseAssistantMention} from './composer-selectors.mjs';
import {assistantMention} from './assistant-mention.mjs';

// Native DOM Markdown. Raw HTML is literal; link protocols are allowlisted.
export function allowedLink(value) {
  try { return ['http:', 'https:'].includes(new URL(value).protocol); }
  catch { return false; }
}
export function parseFileLink(value) {
  const match=String(value).match(/^pot-file:\/\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\/([^?#\\]+)$/i);
  if(!match)return null;
  try {
    const path=decodeURIComponent(match[2]);
    if(new TextEncoder().encode(path).length>4096||/[\\\0]/.test(path)||path.split('/').some(part=>!part||part==='.'||part==='..'))return null;
    return {task:match[1].toLowerCase(),path};
  }catch{return null;}
}
function element(tag,text) {const node=document.createElement(tag);if(text!==undefined)node.textContent=text;return node;}
function inline(parent,source,depth=0,options={}) {
  if(depth>8){parent.append(document.createTextNode(source));return;}
  const pattern=new RegExp('(`+[^`\\n]+`+|'+assistantMentionSource+String.raw`|\*\*[^*\n]+\*\*|__[^_\n]+__|~~[^~\n]+~~|(?<!\*)\*[^*\n]+\*(?!\*)|https?:\/\/[^\s<>]+)`,'gu');
  const tokens=[...source.matchAll(pattern)].map(match=>({start:match.index,end:match.index+match[0].length,token:match[0]}));
  for(const link of inlineLinks(source))tokens.push({...link,token:source.slice(link.start,link.end),link});
  tokens.sort((a,b)=>a.start-b.start||b.end-a.end);
  let offset=0;
  for(const value of tokens){
    if(value.start<offset)continue;
    parent.append(document.createTextNode(source.slice(offset,value.start)));
    const {token,link}=value,mention=parseAssistantMention(token);
    if(link){
      const file=parseFileLink(link.href);
      if(fileHref(link.href)&&options.renderFileReference)parent.append(options.renderFileReference({...link,block:!!options.standalone&&source.trim()===token}));
      else if(file&&options.acquireFileImage&&options.downloadFile&&(link.image||/\.(png|jpe?g|webp|gif)$/i.test(file.path)))parent.append(createFileImage(file,link.label,options));
      else if(file&&options.downloadFile){const a=element('a',link.label||file.path);a.href='#';a.onclick=event=>{event.preventDefault();options.downloadFile(file);};parent.append(a);}
      else if(allowedLink(link.href)){const a=element('a',link.label||link.href);a.href=link.href;a.target='_blank';a.rel='noopener noreferrer';parent.append(a);}
      else parent.append(document.createTextNode(token));
    }else if(token.startsWith('`'))parent.append(element('code',token.replace(/^`+|`+$/g,'')));
    else if(mention)parent.append(assistantMention(mention));
    else if(token.startsWith('**')||token.startsWith('__')){const node=element('strong');inline(node,token.slice(2,-2),depth+1,{...options,standalone:false});parent.append(node);}
    else if(token.startsWith('~~')){const node=element('del');inline(node,token.slice(2,-2),depth+1,{...options,standalone:false});parent.append(node);}
    else if(token.startsWith('*')){const node=element('em');inline(node,token.slice(1,-1),depth+1,{...options,standalone:false});parent.append(node);}
    else if(allowedLink(token)){const a=element('a',token);a.href=token;a.target='_blank';a.rel='noopener noreferrer';parent.append(a);}
    else parent.append(document.createTextNode(token));
    offset=value.end;
  }
  parent.append(document.createTextNode(source.slice(offset)));
}
const isBlock=line=>/^( {0,3}#{1,6}\s|\s*(```|~~~)|\s*[-*+]\s|\s*\d+[.)]\s|\s*>|\s*([-*_])(?:\s*\3){2,}\s*$)/.test(line);
export function hasOpenMermaidFence(source){
  let fence;
  for(const raw of String(source).replace(/\r\n/g,'\n').split('\n')){
    const line=raw.replace(/^(?:\s*> ?)+/,'');
    if(fence){if(new RegExp('^\\s*'+fence.mark[0]+'{'+fence.mark.length+',}\\s*$').test(line))fence=undefined;}
    else{const match=line.match(/^\s*(`{3,}|~{3,})(.*)$/);if(match)fence={mark:match[1],diagram:match[2].trim().split(/\s+/)[0].toLowerCase()==='mermaid'};}
  }
  return !!fence?.diagram;
}
export function renderMarkdown(source,options={}) {
  const result=document.createDocumentFragment(),lines=String(source).replace(/\r\n/g,'\n').split('\n');
  let offset=0;const offsets=lines.map(line=>{const at=offset;offset+=line.length+1;return at;});
  const append=(node,index)=>{node.markdownOffset=offsets[index];result.append(node);};
  for(let index=0;index<lines.length;) {
    const start=index;
    const line=lines[index];if(!line.trim()){index++;continue;}
    const fence=line.match(/^\s*(`{3,}|~{3,})(.*)$/);
    if(fence){
      const pre=element('pre'),language=element('span',fence[2].trim()),code=element('code'),body=[];
      const closing=new RegExp('^\\s*'+fence[1][0]+'{'+fence[1].length+',}\\s*$');
      for(index++;index<lines.length&&!closing.test(lines[index]);index++)body.push(lines[index]);
      const closed=index<lines.length;index++;
      language.className='code-language';code.textContent=body.join('\n');pre.append(language,code);
      const diagram=fence[2].trim().split(/\s+/)[0].toLowerCase()==='mermaid';
      // A known asynchronous block is not ready merely because its temporary
      // source can be measured. Closing the fence preserves its slot/deadline.
      append(diagram?createMermaidBlock(code.textContent,pre,{closed,slot:(options.markdownSlot||'markdown')+':'+((options.markdownBaseOffset||0)+offsets[start])}):pre,start);
      continue;
    }
    if(/^(?: {4}|\t)/.test(line)){const pre=element('pre'),code=element('code'),body=[];while(index<lines.length&&(/^(?: {4}|\t)/.test(lines[index])||!lines[index].trim()))body.push(lines[index++].replace(/^(?: {4}|\t)/,''));code.textContent=body.join('\n').trimEnd();pre.append(code);append(pre,start);continue;}
    const heading=line.match(/^ {0,3}(#{1,6})\s+(.+?)(?:\s+#+)?$/);
    if(heading){const node=element(`h${heading[1].length}`);inline(node,heading[2],0,options);append(node,start);index++;continue;}
    if(/^\s*([-*_])(?:\s*\1){2,}\s*$/.test(line)){append(element('hr'),start);index++;continue;}
    if(/^\s*>/.test(line)){const node=element('blockquote'),body=[];while(index<lines.length&&/^\s*>/.test(lines[index]))body.push(lines[index++].replace(/^\s*> ?/,''));node.append(renderMarkdown(body.join('\n'),{...options,markdownSlot:(options.markdownSlot||'markdown')+':quote:'+((options.markdownBaseOffset||0)+offsets[start]),markdownBaseOffset:0}));append(node,start);continue;}
    if(/^\s*(?:[-*+]\s|\d+[.)]\s)/.test(line)) {
      const ordered=/^\s*\d/.test(line),list=element(ordered?'ol':'ul'),base=line.match(/^\s*/)[0].length;
      if(ordered)list.start=parseInt(line.trimStart(),10);
      while(index<lines.length) {
        const match=lines[index].match(/^(\s*)(?:[-*+]|\d+[.)])\s+(.*)$/);if(!match||match[1].length!==base||/^\s*\d/.test(lines[index])!==ordered)break;
        const node=element('li'),text=match[2],task=text.match(/^\[([ xX])\]\s+(.*)$/);index++;
        if(task){const check=element('input');check.type='checkbox';check.disabled=true;check.checked=task[1]!==' ';node.append(check);inline(node,task[2],0,options);}else inline(node,text,0,options);
        const nestedStart=index,nested=[];while(index<lines.length&&lines[index].trim()&&lines[index].match(/^\s*/)[0].length>base)nested.push(lines[index++].slice(base+2));if(nested.length)node.append(renderMarkdown(nested.join('\n'),{...options,markdownSlot:(options.markdownSlot||'markdown')+':list:'+((options.markdownBaseOffset||0)+offsets[nestedStart]),markdownBaseOffset:0}));list.append(node);
      }append(list,start);continue;
    }
    if(index+1<lines.length&&line.includes('|')&&/^\s*\|?\s*:?-{3,}/.test(lines[index+1])) {
      const table=element('table'),head=element('thead'),row=element('tr'),split=value=>value.trim().replace(/^\||\|$/g,'').split('|').map(value=>value.trim());
      for(const cell of split(line)){const th=element('th');inline(th,cell,0,options);row.append(th);}head.append(row);table.append(head);index+=2;const body=element('tbody');
      while(index<lines.length&&lines[index].includes('|')&&lines[index].trim()){const row=element('tr');for(const cell of split(lines[index++])){const td=element('td');inline(td,cell,0,options);row.append(td);}body.append(row);}table.append(body);const wrap=element('div');wrap.className='markdown-table';wrap.append(table);append(wrap,start);continue;
    }
    const paragraph=element('p'),body=[line];for(index++;index<lines.length&&lines[index].trim()&&!isBlock(lines[index]);index++) {if(index+1<lines.length&&lines[index].includes('|')&&/^\s*\|?\s*:?-{3,}/.test(lines[index+1]))break;body.push(lines[index]);}
    const joined=body.join('\n').split(/ {2,}\n|\\\n/);for(const [partIndex,part]of joined.entries()){if(partIndex)paragraph.append(element('br'));inline(paragraph,part.replace(/\n/g,' '),0,{...options,standalone:body.length===1});}append(paragraph,start);
  }return result;
}

// Appended output reparses only the last (potentially incomplete) block. Keep
// completed blocks and live media, selections and code/table scroll positions.
export function createMarkdownView(host,{slot='markdown'}={}) {
  let source='',blocks=[],revision;
  const preparation=currentPreparedRender(),member=preparation?.member||host.closest('article[data-message-id]')?.dataset.messageId||'';
  host.dataset.renderSlot=slot;host.dataset.renderMember=member;
  const previous=preparation?.context.phase==='prepare'?[...preparedPreviousRoot(preparation.context,member)?.querySelectorAll('[data-render-slot]')||[]].find(node=>node.dataset.renderSlot===slot&&node.dataset.renderMember===member)?.preparedMarkdownView:undefined;
  if(previous){source=previous.source;revision=previous.revision;blocks=previous.blocks.map(block=>{const copy=staticPreparationClone(block);copy.markdownOffset=block.markdownOffset;host.append(copy);return copy;});}
  const state={get source(){return source;},get revision(){return revision;},get blocks(){return blocks;}};host.preparedMarkdownView=state;
  function original(next){
    if(currentPreparedRender()?.context.phase==='prepare')return next;
    if(next.preparationOriginal&&next.preparationUpdates)next.preparationOriginal.updatePreparedProperties?.(next.preparationUpdates);
    return next.preparationOriginal||next;
  }
  function adopt(node){
    for(const child of [...node.childNodes]){const value=original(child);if(value!==child){moveConversationNode(node,value,child);child.remove();}else if(child.nodeType===1)adopt(child);}
    return node;
  }
  function patch(old,next) {
    next=original(next);
    if(old===next)return old;
    if(!old||old.nodeType!==next.nodeType||old.nodeName!==next.nodeName)return adopt(next);
    if(old.nodeType===3){
      if(old.data===next.data)return old;
      const selection=getSelection(),endpoints=selection?.rangeCount?[selection.anchorNode,selection.anchorOffset,selection.focusNode,selection.focusOffset]:null;
      if(endpoints&&old.parentNode&&[0,2].every(index=>endpoints[index]!==old||old.data.slice(0,endpoints[index+1])===next.data.slice(0,endpoints[index+1]))){
        moveConversationNode(old.parentNode,next,old);
        selection.setBaseAndExtent(...endpoints.map((value,index)=>index%2===0&&value===old?next:value));
      }
      return next;
    }
    if(old.localName==='pot-message-file'){if(old.identity!==next.identity)return next;if(next.preparationUpdates)old.preparationUpdates=next.preparationUpdates;return old;}
    if(old.localName==='pot-mermaid')return old.sourceText===next.sourceText&&old.closed===next.closed?old:next;
    if(old.localName==='pot-file-image')return old.file.task===next.file.task&&old.file.path===next.file.path&&old.label===next.label?old:next;
    if(old.isEqualNode(next))return old;
    for(const attr of [...old.attributes])if(!next.hasAttribute(attr.name))old.removeAttribute(attr.name);
    for(const attr of next.attributes)if(old.getAttribute(attr.name)!==attr.value)old.setAttribute(attr.name,attr.value);
    old.onclick=next.onclick;
    if(old.localName==='input')old.checked=next.checked;
    const children=[...next.childNodes];
    for(const [index,child]of children.entries()){
      const current=old.childNodes[index],retained=patch(current,child);
      if(retained!==current){moveConversationNode(old,retained,current||null);current?.remove();}
    }
    while(old.childNodes.length>children.length)old.lastChild.remove();
    return old;
  }
  function apply(nodes,retained=0,offset=0){
    for(const [index,node]of nodes.entries()){
      const position=retained+index,current=blocks[position],at=offset+(node.markdownOffset||0),value=patch(current,node);
      value.markdownOffset=at;
      if(value!==current){moveConversationNode(host,value,current||null);current?.remove();}
      blocks[position]=value;
    }
    for(const block of blocks.splice(retained+nodes.length))block.remove();
  }
  return {update(text,options={}){
    const next=String(text||'').replace(/\r\n/g,'\n'),version=JSON.stringify([next,options.revision]),render=currentPreparedRender();
    if(render?.context.phase==='prepare'&&hasOpenMermaidFence(next))render.context.incompleteSources.set(member+':'+slot,{member,source:next});
    if(render?.context.phase==='commit'){
      if(next===source&&revision===options.revision)return;
      const candidate=preparedAsset(slot,version,()=>{throw Error('Missing prepared Markdown');});
      apply(candidate.nodes);source=next;revision=options.revision;return;
    }
    if(next!==source||revision!==options.revision){
      const retained=revision===options.revision&&next.startsWith(source)&&blocks.length?blocks.length-1:0;
      const offset=retained?blocks[retained].markdownOffset:0;
      apply([...renderMarkdown(next.slice(offset),{...options,markdownSlot:slot,markdownBaseOffset:offset}).childNodes],retained,offset);
      source=next;revision=options.revision;
    }
    if(render?.context.phase==='prepare')preparedAsset(slot,version,()=>({nodes:[...host.childNodes]}));
  }};
}

const views=new WeakMap();
export function updateMarkdown(host,text,options={}){
  let view=views.get(host);if(!view){view=createMarkdownView(host,{slot:options.renderSlot||'markdown'});views.set(host,view);}
  view.update(text,options);
}
