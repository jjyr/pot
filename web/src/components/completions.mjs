import {translate} from '../locales/messages.mjs';
import {avatar} from './avatar.mjs';
import {node} from './primitives.mjs';
import {icon} from './icons.mjs';
import {selectorText,splitComposerValue,joinComposerValue} from './composer-selectors.mjs';
import {bindComposerMentions} from './composer-mentions.mjs';

export function completionQuery(text,caret,end=caret){
  if(caret!==end)return null;
  const match=text.slice(0,caret).match(/(?:^|\s)([/@][^\s/@]*)$/u);
  return match?{start:caret-match[1].length,end:caret,query:match[1]}:null;
}
export function bindCompletions(input,{client,host,locale,mobile=false,id='pot-reference-completions',setText=value=>{input.value=value;}}){
  const t=key=>translate(locale(),key),form=input.form||input.closest('form');
  const menu=node('div','completion-list'),heading=node('div','completion-heading'),title=node('span'),status=node('span','completion-status'),list=node('div','completion-options'),footer=node('div','completion-footer');
  menu.id=id;menu.hidden=true;list.id=id+'-options';list.setAttribute('role','listbox');heading.append(title,status);menu.append(heading,list,footer);form.append(menu);
  const chips=node('div','composer-selectors'),triggers=node('div','composer-selectors-triggers');chips.hidden=true;chips.setAttribute('aria-live','polite');form.classList.add('has-completions');form.prepend(chips);form.append(triggers);
  input.setAttribute('aria-autocomplete','list');input.setAttribute('aria-controls',list.id);input.setAttribute('aria-expanded','false');
  let items=[],selected=[],index=0,active=null,composing=false,ended=-Infinity,abort,version=0,lastLocale=locale(),disposed=false;
  const metadata=new Map(),mentions=bindComposerMentions(input,{setText});
  const value=()=>joinComposerValue(selected,mentions.value());
  const emit=()=>input.dispatchEvent(new Event('input',{bubbles:true}));
  const close=()=>{version++;abort?.abort();items=[];active=null;menu.hidden=true;input.setAttribute('aria-expanded','false');input.removeAttribute('aria-activedescendant');for(const button of triggers.children)button.setAttribute('aria-pressed','false');};
  const optionID=offset=>`${id.replace(/-completions$/,'')}-${offset}`;
  function paintChips(){
    chips.replaceChildren();chips.hidden=!selected.length;
    selected.forEach((item,offset)=>{const chip=node('span','composer-selector');chip.title=item.token;
      chip.append(node('span','composer-selector-name','/'+item.label));
      const remove=node('button','composer-selector-remove');remove.type='button';remove.disabled=input.disabled||input.readOnly;remove.setAttribute('aria-label',t('completions.remove').replace('{name}',item.label));remove.append(icon('close'));remove.onclick=()=>{if(composing||input.disabled||input.readOnly)return;selected.splice(offset,1);paintChips();emit();input.focus({preventScroll:true});};chip.append(remove);chips.append(chip);
    });
  }
  function choose(item){
    if(!active||menu.hidden||!items.includes(item)||composing||input.disabled||input.readOnly||item.offline)return;
    const query=completionQuery(input.value,input.selectionStart,input.selectionEnd);
    if(!query||query.start!==active.start||query.query!==active.query)return close();
    const raw=selectorText(item);if(!raw)return close();
    if(item.token.startsWith('@assistant:')){
      // Native selection retains surrounding text and moves the caret after the
      // inline reference, ready for more typing. Each occurrence stays in place.
      close();mentions.replace(raw+' ',query.start,query.end);
    }else{
      input.setRangeText('',query.start,query.end,'end');
      if(!selected.some(old=>old.token===item.token))selected.push({...item,raw});
    }
    close();paintChips();emit();input.focus({preventScroll:true});
  }
  function paint(){
    const assistant=active.query.startsWith('@');
    title.textContent=t(assistant?'completions.assistants':'completions.skills');list.setAttribute('aria-label',title.textContent);status.textContent=t('completions.count').replace('{count}',items.length.toLocaleString(locale()));
    list.replaceChildren();items.forEach((item,offset)=>{const option=node('button','completion-option');option.type='button';option.id=optionID(offset);option.setAttribute('role','option');option.setAttribute('aria-selected',String(offset===index));option.setAttribute('aria-disabled',String(!!item.offline));
      if(assistant)option.append(avatar(20,item.avatar));
      const description=item.local?t('completions.local'):item.detail||'',text=node('span','completion-text'),name=node('span','completion-name',item.label||item.token),detail=node('small','completion-detail',description);
      text.append(name);if(description)text.append(detail);option.append(text);option.title=[item.label,description,item.token].filter(Boolean).join('\n');
      if(item.offline)option.append(node('small','completion-offline',t('completions.offline')));
      else if(offset===index){const enter=node('span','completion-enter','↵');enter.setAttribute('aria-hidden','true');option.append(enter);}
      option.onpointerdown=event=>event.preventDefault();option.onclick=()=>choose(item);list.append(option);
    });
    if(!items.length)list.append(node('div','completion-empty',t(assistant?'completions.noAssistants':'completions.noSkills')));
    footer.replaceChildren();footer.hidden=mobile;for(const [key,label]of [['↑ ↓','completions.navigate'],['Enter','completions.insert'],['Esc','completions.close']]){const hint=node('span');hint.append(node('kbd','',key),document.createTextNode(t(label)));footer.append(hint);}
    menu.hidden=false;input.setAttribute('aria-expanded','true');if(items.length&&index>=0){input.setAttribute('aria-activedescendant',optionID(index));list.children[index]?.scrollIntoView({block:'nearest'});}else input.removeAttribute('aria-activedescendant');
    for(const button of triggers.children)button.setAttribute('aria-pressed',String(button.dataset.trigger===active.query[0]));
  }
  async function update(){
    if(disposed||composing||input.disabled||input.readOnly||document.activeElement!==input)return close();const query=completionQuery(input.value,input.selectionStart,input.selectionEnd);if(!query)return close();
    if(active?.query===query.query&&active.start===query.start)return;
    close();active=query;const ticket=version,scope=client.scope(),key=JSON.stringify(scope);abort=new AbortController();
    try{const result=await (client.read||client.request)('/api/v1/completions?'+new URLSearchParams({q:query.query}),{...scope,signal:abort.signal});if(disposed||ticket!==version||JSON.stringify(client.scope())!==key||composing)return;const current=completionQuery(input.value,input.selectionStart,input.selectionEnd);if(!current||current.query!==query.query||current.start!==query.start)return close();
      const pattern=query.query.startsWith('@')?/^@assistant:[A-Za-z0-9_-]{1,128}\/[A-Za-z0-9_-]{1,128}$/u:/^\/skill:[a-z0-9_-]{1,64}$/u;
      items=(result.items||[]).filter(item=>typeof item.token==='string'&&pattern.test(item.token)).slice(0,50);
      for(const item of items)metadata.set(item.token,item);
      index=items.findIndex(item=>!item.offline);paint();
    }catch(reason){if(ticket===version&&reason.name!=='AbortError')close();}
  }
  function key(event){if(composing||event.isComposing||event.keyCode===229||performance.now()-ended<80||menu.hidden)return;if(event.key==='Escape'){event.preventDefault();event.stopImmediatePropagation();close();}else if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();event.stopImmediatePropagation();const available=items.flatMap((item,i)=>item.offline?[]:[i]);if(available.length){index=available[(available.indexOf(index)+(event.key==='ArrowDown'?1:available.length-1))%available.length];paint();}}else if(!mobile&&event.key==='Enter'&&!event.shiftKey&&!event.metaKey&&!event.ctrlKey&&!event.altKey){event.preventDefault();event.stopImmediatePropagation();if(items[index])choose(items[index]);}else if(event.key==='Tab')close();}
  function refresh(){
    const changed=lastLocale!==locale();lastLocale=locale();
    for(const button of triggers.children){button.disabled=input.disabled||input.readOnly;button.lastChild.textContent=t(button.dataset.trigger==='/'?'completions.skills':'completions.assistants');}
    for(const remove of chips.querySelectorAll('button'))remove.disabled=input.disabled||input.readOnly;
    if(changed){paintChips();if(active&&!menu.hidden)paint();}
  }
  for(const trigger of ['/', '@']){const button=node('button','composer-selector-trigger');button.type='button';button.dataset.trigger=trigger;button.setAttribute('aria-pressed','false');button.append(node('span','composer-selector-symbol',trigger),node('span'));button.onclick=()=>{if(composing||input.disabled||input.readOnly)return;const query=completionQuery(input.value,input.selectionStart,input.selectionEnd),at=input.selectionStart;input.setRangeText(query?trigger:(at&&!/\s/u.test(input.value[at-1])?' ':'')+trigger,query?.start??at,query?.end??input.selectionEnd,'end');input.focus({preventScroll:true});emit();};triggers.append(button);}
  const start=()=>{composing=true;close();},end=()=>{composing=false;ended=performance.now();update();},blur=event=>{if(!menu.contains(event.relatedTarget))close();},keyup=event=>{if(event.key!=='Escape')update();};
  const outside=event=>{if(!host.contains(event.target))close();};
  const stopInvalidations=client.resources?.subscribeInvalidation((topics,source={})=>{
    if(!active?.query.startsWith('@')||source.space&&source.space!==client.scope().space)return;
    if(topics.length&&!topics.some(topic=>['assistants','agent-links','spaces','runtime'].includes(topic))&&!source.reset)return;
    // Retire both visible options and the request generation immediately. Keep
    // draft text, existing references, selection and Skills exactly as entered.
    close();update();
  });
  input.addEventListener('input',update);input.addEventListener('click',update);input.addEventListener('keyup',keyup);input.addEventListener('select',update);input.addEventListener('keydown',key,true);input.addEventListener('compositionstart',start);input.addEventListener('compositionend',end);input.addEventListener('blur',blur);document.addEventListener('pointerdown',outside);window.addEventListener('pot:locale',refresh);
  refresh();
  return {value,refresh,reset:close,setValue(text){if(composing||text===value()&&(selected.length||!splitComposerValue(text).items.length))return;close();const draft=splitComposerValue(text);selected=draft.items.map(item=>({...metadata.get(item.token),...item}));mentions.setValue(draft.text);paintChips();},dispose(){disposed=true;stopInvalidations?.();close();mentions.dispose();menu.remove();chips.remove();triggers.remove();form.classList.remove('has-completions');for(const event of ['input','click','select'])input.removeEventListener(event,update);input.removeEventListener('keyup',keyup);input.removeEventListener('keydown',key,true);input.removeEventListener('compositionstart',start);input.removeEventListener('compositionend',end);input.removeEventListener('blur',blur);document.removeEventListener('pointerdown',outside);window.removeEventListener('pot:locale',refresh);}};
}
