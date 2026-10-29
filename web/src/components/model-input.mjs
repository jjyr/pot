import {translate} from '../locales/messages.mjs';

// A native editable combobox keeps typing, selection and IME in the same input.
export function bindModelInput(input,locale) {
  const list=document.createElement('datalist');list.id='model-options-'+crypto.randomUUID();
  input.after(list);input.setAttribute('list',list.id);input.autocomplete='off';input.spellcheck=false;
  // Native datalists filter by the current value, often hiding every other ID.
  // Keep a separate unfiltered picker without disturbing the editable input.
  const browse=document.createElement('select'),prompt=document.createElement('option');
  browse.className='control model-browse';prompt.value='';browse.append(prompt);list.after(browse);
  const sync=()=>{browse.disabled=input.disabled||input.readOnly||browse.options.length<2;};
  const observer=new MutationObserver(sync);observer.observe(input,{attributes:true,attributeFilter:['disabled','readonly']});
  browse.addEventListener('change',()=>{
    const value=browse.value;browse.value='';
    if(!value||input.disabled||input.readOnly)return;
    input.value=value;input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));
  });
  const refreshLocale=()=>{input.placeholder=translate(locale(),'assistant.modelInput');prompt.textContent=translate(locale(),'assistant.browseModels');browse.setAttribute('aria-label',prompt.textContent);};refreshLocale();sync();
  return {refreshLocale,dispose(){observer.disconnect();browse.remove();list.remove();input.removeAttribute('list');},update(models){
    list.replaceChildren(...models.map(model=>{const option=document.createElement('option');option.value=model.id;if(model.name&&model.name!==model.id)option.label=model.name;return option;}));
    browse.replaceChildren(prompt,...models.map(model=>{const option=document.createElement('option');option.value=model.id;option.textContent=model.name&&model.name!==model.id?`${model.name} (${model.id})`:model.id;return option;}));sync();
  }};
}
