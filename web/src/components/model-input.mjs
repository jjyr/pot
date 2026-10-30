import {translate} from '../locales/messages.mjs';

// Keep one editable input; opening the picker does not filter by its saved ID.
export function bindModelInput(input,locale) {
  const wrap=document.createElement('div'),toggle=document.createElement('button'),list=document.createElement('div');
  wrap.className='model-picker';toggle.className='model-picker-toggle';toggle.type='button';toggle.textContent='▾';
  list.className='model-options';list.id='model-options-'+crypto.randomUUID();list.setAttribute('role','listbox');list.hidden=true;
  input.before(wrap);wrap.append(input,toggle,list);
  input.autocomplete='off';input.spellcheck=false;input.setAttribute('role','combobox');input.setAttribute('aria-autocomplete','list');input.setAttribute('aria-controls',list.id);input.setAttribute('aria-expanded','false');
  let models=[],filtered=[],active=-1,query='',open=false,selecting=false,composing=false;
  const listeners=[];
  const on=(node,event,handler)=>{node.addEventListener(event,handler);listeners.push(()=>node.removeEventListener(event,handler));};
  const enabled=()=>!input.disabled&&!input.readOnly;
  const close=()=>{open=false;list.hidden=true;input.setAttribute('aria-expanded','false');input.removeAttribute('aria-activedescendant');};
  const render=()=>{
    filtered=models.filter(model=>!query||`${model.id} ${model.name||''}`.toLowerCase().includes(query));
    if(active>=filtered.length)active=-1;
    list.replaceChildren(...filtered.map((model,index)=>{
      const option=document.createElement('div');option.id=list.id+'-'+index;option.setAttribute('role','option');option.setAttribute('aria-selected',String(index===active));
      option.textContent=model.name&&model.name!==model.id?`${model.name} (${model.id})`:model.id;
      option.addEventListener('pointerdown',event=>event.preventDefault());option.addEventListener('click',event=>{event.preventDefault();select(model.id);});return option;
    }));
    list.hidden=!open||!filtered.length;input.setAttribute('aria-expanded',String(!list.hidden));
    if(!list.hidden&&active>=0)input.setAttribute('aria-activedescendant',list.children[active].id);else input.removeAttribute('aria-activedescendant');
  };
  const show=()=>{if(!enabled()||selecting)return;query='';active=-1;open=true;render();};
  const select=value=>{
    if(!enabled())return;
    selecting=true;input.value=value;input.focus();close();
    input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));selecting=false;
  };
  on(input,'focus',show);on(input,'click',show);
  on(input,'input',()=>{if(selecting||composing||!enabled())return;query=input.value.toLowerCase();active=-1;open=true;render();});
  on(input,'compositionstart',()=>{composing=true;});
  on(input,'compositionend',()=>{composing=false;if(enabled()){query=input.value.toLowerCase();active=-1;open=true;render();}});
  on(input,'keydown',event=>{
    if(event.isComposing||composing||!enabled())return;
    if(event.key==='ArrowDown'||event.key==='ArrowUp'){
      event.preventDefault();if(!open)show();if(!filtered.length)return;
      active=active<0?(event.key==='ArrowDown'?0:filtered.length-1):(active+(event.key==='ArrowDown'?1:-1)+filtered.length)%filtered.length;
      render();list.children[active]?.scrollIntoView({block:'nearest'});
    }else if(event.key==='Enter'&&open&&active>=0){event.preventDefault();select(filtered[active].id);}
    else if(event.key==='Escape'&&open){event.preventDefault();event.stopPropagation();close();}
    else if(event.key==='Tab')close();
  });
  on(toggle,'pointerdown',event=>event.preventDefault());
  on(toggle,'click',()=>{if(!enabled())return;if(open)close();else{input.focus();show();}});
  on(document,'pointerdown',event=>{if(!wrap.contains(event.target))close();});
  on(wrap,'focusout',event=>{if(!wrap.contains(event.relatedTarget))close();});
  const sync=()=>{toggle.disabled=!enabled();if(!enabled())close();};
  const observer=new MutationObserver(sync);observer.observe(input,{attributes:true,attributeFilter:['disabled','readonly']});
  const refreshLocale=()=>{input.placeholder=translate(locale(),'assistant.modelInput');const label=translate(locale(),'assistant.browseModels');toggle.setAttribute('aria-label',label);list.setAttribute('aria-label',label);};
  refreshLocale();sync();
  return {refreshLocale,update(values){models=values;render();},dispose(){observer.disconnect();listeners.forEach(remove=>remove());wrap.replaceWith(input);for(const name of ['role','aria-autocomplete','aria-controls','aria-expanded','aria-activedescendant'])input.removeAttribute(name);}};
}
