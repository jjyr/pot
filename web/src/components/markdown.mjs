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
  const pattern=new RegExp('(`+[^`\\n]+`+|'+assistantMentionSource+String.raw`|!\[[^\]\n]*\]\([^\s)]+\)|\*\*[^*\n]+\*\*|__[^_\n]+__|~~[^~\n]+~~|(?<!\*)\*[^*\n]+\*(?!\*)|\[[^\]\n]+\]\([^\s)]+\)|https?:\/\/[^\s<>]+)`,'gu');
  let offset=0;
  for(const match of source.matchAll(pattern)) {
    parent.append(document.createTextNode(source.slice(offset,match.index)));const token=match[0],mention=parseAssistantMention(token);
    if(token.startsWith('`'))parent.append(element('code',token.replace(/^`+|`+$/g,'')));
    else if(mention)parent.append(assistantMention(mention));
    else if(token.startsWith('**')||token.startsWith('__')){const node=element('strong');inline(node,token.slice(2,-2),depth+1,options);parent.append(node);}
    else if(token.startsWith('~~')){const node=element('del');inline(node,token.slice(2,-2),depth+1,options);parent.append(node);}
    else if(token.startsWith('*')){const node=element('em');inline(node,token.slice(1,-1),depth+1,options);parent.append(node);}
    else if(token.startsWith('![')) {
      // Remote images are links until explicitly opened, avoiding invisible
      // requests from model content and matching host-controlled navigation.
      const [,label,href]=token.match(/^!\[([^\]]*)\]\((.+)\)$/);
      const file=parseFileLink(href);
      if(file&&options.acquireFileImage&&options.downloadFile)parent.append(createFileImage(file,label,options));
      else if(file&&options.downloadFile){const link=element('a',label||file.path);link.href='#';link.onclick=event=>{event.preventDefault();options.downloadFile(file);};parent.append(link);}
      else if(allowedLink(href)){const link=element('a',label||href);link.href=href;link.target='_blank';link.rel='noopener noreferrer';parent.append(link);}else parent.append(document.createTextNode(token));
    }else {
      const parsed=token.startsWith('[')?token.match(/^\[([^\]]+)\]\((.+)\)$/):[null,token,token];
      const [,label,href]=parsed;
      const file=parseFileLink(href);
      if(file&&options.acquireFileImage&&options.downloadFile&&/\.(png|jpe?g|webp|gif)$/i.test(file.path))parent.append(createFileImage(file,label,options));
      else if(file&&options.downloadFile){const link=element('a',label);link.href='#';link.onclick=event=>{event.preventDefault();options.downloadFile(file);};parent.append(link);}
      else if(allowedLink(href)){const link=element('a',label);link.href=href;link.target='_blank';link.rel='noopener noreferrer';parent.append(link);}else parent.append(document.createTextNode(token));
    }
    offset=match.index+token.length;
  }
  parent.append(document.createTextNode(source.slice(offset)));
}
const isBlock=line=>/^( {0,3}#{1,6}\s|\s*(```|~~~)|\s*[-*+]\s|\s*\d+[.)]\s|\s*>|\s*([-*_])(?:\s*\3){2,}\s*$)/.test(line);
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
      // Incomplete streamed fences remain ordinary code until their closing line.
      append(closed&&fence[2].trim().split(/\s+/)[0].toLowerCase()==='mermaid'?createMermaidBlock(code.textContent,pre):pre,start);
      continue;
    }
    const heading=line.match(/^ {0,3}(#{1,6})\s+(.+?)(?:\s+#+)?$/);
    if(heading){const node=element(`h${heading[1].length}`);inline(node,heading[2],0,options);append(node,start);index++;continue;}
    if(/^\s*([-*_])(?:\s*\1){2,}\s*$/.test(line)){append(element('hr'),start);index++;continue;}
    if(/^\s*>/.test(line)){const node=element('blockquote'),body=[];while(index<lines.length&&/^\s*>/.test(lines[index]))body.push(lines[index++].replace(/^\s*> ?/,''));node.append(renderMarkdown(body.join('\n'),options));append(node,start);continue;}
    if(/^\s*(?:[-*+]\s|\d+[.)]\s)/.test(line)) {
      const ordered=/^\s*\d/.test(line),list=element(ordered?'ol':'ul'),base=line.match(/^\s*/)[0].length;
      if(ordered)list.start=parseInt(line.trimStart(),10);
      while(index<lines.length) {
        const match=lines[index].match(/^(\s*)(?:[-*+]|\d+[.)])\s+(.*)$/);if(!match||match[1].length!==base||/^\s*\d/.test(lines[index])!==ordered)break;
        const node=element('li'),text=match[2],task=text.match(/^\[([ xX])\]\s+(.*)$/);index++;
        if(task){const check=element('input');check.type='checkbox';check.disabled=true;check.checked=task[1]!==' ';node.append(check);inline(node,task[2],0,options);}else inline(node,text,0,options);
        const nested=[];while(index<lines.length&&lines[index].trim()&&lines[index].match(/^\s*/)[0].length>base)nested.push(lines[index++].slice(base+2));if(nested.length)node.append(renderMarkdown(nested.join('\n'),options));list.append(node);
      }append(list,start);continue;
    }
    if(index+1<lines.length&&line.includes('|')&&/^\s*\|?\s*:?-{3,}/.test(lines[index+1])) {
      const table=element('table'),head=element('thead'),row=element('tr'),split=value=>value.trim().replace(/^\||\|$/g,'').split('|').map(value=>value.trim());
      for(const cell of split(line)){const th=element('th');inline(th,cell,0,options);row.append(th);}head.append(row);table.append(head);index+=2;const body=element('tbody');
      while(index<lines.length&&lines[index].includes('|')&&lines[index].trim()){const row=element('tr');for(const cell of split(lines[index++])){const td=element('td');inline(td,cell,0,options);row.append(td);}body.append(row);}table.append(body);const wrap=element('div');wrap.className='markdown-table';wrap.append(table);append(wrap,start);continue;
    }
    const paragraph=element('p'),body=[line];for(index++;index<lines.length&&lines[index].trim()&&!isBlock(lines[index]);index++) {if(index+1<lines.length&&lines[index].includes('|')&&/^\s*\|?\s*:?-{3,}/.test(lines[index+1]))break;body.push(lines[index]);}
    const joined=body.join('\n').split(/ {2,}\n|\\\n/);for(const [partIndex,part]of joined.entries()){if(partIndex)paragraph.append(element('br'));inline(paragraph,part.replace(/\n/g,' '),0,options);}append(paragraph,start);
  }return result;
}

// Appended output reparses only the last (potentially incomplete) block. Keep
// completed blocks and live media, selections and code/table scroll positions.
export function createMarkdownView(host) {
  let source='',blocks=[];
  function patch(old,next) {
    if(!old||old.nodeType!==next.nodeType||old.nodeName!==next.nodeName)return next;
    if(old.nodeType===3){if(old.data!==next.data)old.data=next.data;return old;}
    if(old.localName==='pot-mermaid')return old.sourceText===next.sourceText?old:next;
    if(old.localName==='pot-file-image')return old.file.task===next.file.task&&old.file.path===next.file.path&&old.label===next.label?old:next;
    if(old.isEqualNode(next))return old;
    for(const attr of [...old.attributes])if(!next.hasAttribute(attr.name))old.removeAttribute(attr.name);
    for(const attr of next.attributes)if(old.getAttribute(attr.name)!==attr.value)old.setAttribute(attr.name,attr.value);
    old.onclick=next.onclick;
    if(old.localName==='input')old.checked=next.checked;
    const children=[...next.childNodes];
    for(const [index,child]of children.entries()){
      const current=old.childNodes[index],retained=patch(current,child);
      if(retained!==current){if(current)current.replaceWith(retained);else old.append(retained);}
    }
    while(old.childNodes.length>children.length)old.lastChild.remove();
    return old;
  }
  return {update(text,options={}){
    const next=String(text||'').replace(/\r\n/g,'\n');if(next===source)return;
    const retained=next.startsWith(source)&&blocks.length?blocks.length-1:0;
    const offset=retained?blocks[retained].markdownOffset:0;
    const tail=[...renderMarkdown(next.slice(offset),options).childNodes];
    for(const [index,node]of tail.entries()){
      const position=retained+index,current=blocks[position],at=offset+node.markdownOffset,value=patch(current,node);
      value.markdownOffset=at;
      if(value!==current){if(current)current.replaceWith(value);else host.append(value);}
      blocks[position]=value;
    }
    for(const block of blocks.splice(retained+tail.length))block.remove();
    source=next;
  }};
}
