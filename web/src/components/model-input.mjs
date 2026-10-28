import {translate} from '../locales/messages.mjs';

// A native editable combobox keeps typing, selection and IME in the same input.
export function bindModelInput(input,locale) {
  const list=document.createElement('datalist');list.id='model-options-'+crypto.randomUUID();
  input.after(list);input.setAttribute('list',list.id);input.autocomplete='off';input.spellcheck=false;
  const refreshLocale=()=>{input.placeholder=translate(locale(),'assistant.modelInput');};refreshLocale();
  return {refreshLocale,update(models){
    list.replaceChildren(...models.map(model=>{const option=document.createElement('option');option.value=model.id;if(model.name&&model.name!==model.id)option.label=model.name;return option;}));
  }};
}
