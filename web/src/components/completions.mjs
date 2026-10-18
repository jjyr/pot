export function completionQuery(text,caret,end=caret){
  if(caret!==end)return null;
  const match=text.slice(0,caret).match(/(?:^|\s)(\/[^\s/]*)$/u);
  return match?{start:caret-match[1].length,end:caret,query:match[1]}:null;
}
export function bindCompletions(input,{client,host,locale,id='pot-reference-completions'}){
  const menu=document.createElement('div');menu.className='completion-list';menu.id=id;menu.hidden=true;menu.setAttribute('role','listbox');host.append(menu);
  input.setAttribute('aria-autocomplete','list');input.setAttribute('aria-controls',menu.id);
  let items=[],index=0,active=null,composing=false,ended=-Infinity,abort,version=0;
  const close=()=>{version++;abort?.abort();items=[];active=null;menu.hidden=true;input.setAttribute('aria-expanded','false');input.removeAttribute('aria-activedescendant');};
  const choose=item=>{if(!active||composing||input.readOnly)return close();const query=completionQuery(input.value,input.selectionStart,input.selectionEnd);if(!query||query.start!==active.start||query.query!==active.query)return close();input.setRangeText(item.token+' ',query.start,query.end,'end');close();input.dispatchEvent(new Event('input',{bubbles:true}));input.focus({preventScroll:true});};
  const optionID=offset=>`${id.replace(/-completions$/,'')}-${offset}`;
  function paint(){menu.replaceChildren();menu.setAttribute('aria-label',locale()==='zh-CN'?'引用补全':'Reference completions');items.forEach((item,offset)=>{const option=document.createElement('button');option.type='button';option.id=optionID(offset);option.className='completion-option';option.setAttribute('role','option');option.setAttribute('aria-selected',String(offset===index));const name=document.createElement('span'),detail=document.createElement('small');name.textContent=item.label||item.token;detail.textContent=item.detail||item.token;option.append(name,detail);option.onpointerdown=event=>event.preventDefault();option.onclick=()=>choose(item);menu.append(option);});menu.hidden=!items.length;input.setAttribute('aria-expanded',String(!!items.length));if(items.length){input.setAttribute('aria-activedescendant',optionID(index));menu.children[index]?.scrollIntoView({block:'nearest'});}}
  async function update(){
    if(composing||input.disabled||input.readOnly||document.activeElement!==input)return close();const query=completionQuery(input.value,input.selectionStart,input.selectionEnd);if(!query)return close();
    if(active?.query===query.query&&active.start===query.start)return;
    close();active=query;const ticket=version,scope=client.scope(),key=JSON.stringify(scope);abort=new AbortController();
    try{const value=await (client.read||client.request)('/api/v1/completions?'+new URLSearchParams({q:query.query}),{...scope,signal:abort.signal});if(ticket!==version||JSON.stringify(client.scope())!==key||composing)return;const current=completionQuery(input.value,input.selectionStart,input.selectionEnd);if(!current||current.query!==query.query||current.start!==query.start)return close();items=(value.items||[]).filter(item=>typeof item.token==='string'&&/^\/(machine|tag|skill):\S+$/u.test(item.token)).slice(0,50);index=0;paint();}catch(reason){if(reason.name!=='AbortError')close();}
  }
  function key(event){if(composing||event.isComposing||event.keyCode===229||performance.now()-ended<80||menu.hidden)return;if(event.key==='Escape'){event.preventDefault();event.stopImmediatePropagation();close();}else if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();event.stopImmediatePropagation();index=(index+(event.key==='ArrowDown'?1:items.length-1))%items.length;paint();}else if(event.key==='Enter'&&!event.shiftKey&&!event.metaKey&&!event.ctrlKey&&!event.altKey){event.preventDefault();event.stopImmediatePropagation();choose(items[index]);}}
  const start=()=>{composing=true;close();},end=()=>{composing=false;ended=performance.now();update();},blur=()=>close();
  input.addEventListener('input',update);input.addEventListener('click',update);input.addEventListener('keyup',update);input.addEventListener('select',update);input.addEventListener('keydown',key,true);input.addEventListener('compositionstart',start);input.addEventListener('compositionend',end);input.addEventListener('blur',blur);
  return {reset:close,dispose(){close();menu.remove();for(const event of ['input','click','keyup','select'])input.removeEventListener(event,update);input.removeEventListener('keydown',key,true);input.removeEventListener('compositionstart',start);input.removeEventListener('compositionend',end);input.removeEventListener('blur',blur);}};
}
