import {assistantMentionSource,parseAssistantMention} from './composer-selectors.mjs';
import {assistantMention} from './assistant-mention.mjs';

// The textarea edits compact labels. Only these ranges carry identities; typing
// an ordinary @name never guesses a recipient from its display name.
export function decodeMentions(value){
  let text='',offset=0;const mentions=[];
  for(const match of value.matchAll(new RegExp(assistantMentionSource,'gu'))){
    text+=value.slice(offset,match.index);
    const identity=parseAssistantMention(match[0]),start=text.length;
    text+='@'+identity.label;mentions.push({...identity,start,end:text.length,raw:match[0]});offset=match.index+match[0].length;
  }
  return {text:text+value.slice(offset),mentions};
}
export function encodeMentions({text,mentions},start=0,end=text.length){
  let value='',offset=start;
  for(const mention of mentions){
    if(mention.start<start||mention.end>end)continue;
    value+=text.slice(offset,mention.start)+mention.raw;offset=mention.end;
  }
  return value+text.slice(offset,end);
}
export function editMentions(state,text,range){
  let start=range?.start,end=range?.end;
  if(start!==undefined&&(!text.startsWith(state.text.slice(0,start))||!text.endsWith(state.text.slice(end))))start=undefined;
  if(start===undefined){
    start=0;while(start<state.text.length&&start<text.length&&state.text[start]===text[start])start++;
    end=state.text.length;let tail=text.length;
    while(end>start&&tail>start&&state.text[end-1]===text[tail-1]){end--;tail--;}
  }
  const shift=text.length-state.text.length;
  return {text,mentions:state.mentions.flatMap(mention=>{
    if(mention.end<=start)return [mention];
    if(mention.start>=end)return [{...mention,start:mention.start+shift,end:mention.end+shift}];
    return [];
  })};
}

const editors=new WeakMap();
export const composerMentionValue=input=>editors.get(input)?.value()??input.value;

export function bindComposerMentions(input,{setText}){
  const mirror=document.createElement('div');mirror.className='composer-mention-layer';mirror.setAttribute('aria-hidden','true');
  input.before(mirror);
  let state=decodeMentions(input.value),pending=null,composing=false;
  let history=[state],historyIndex=0;
  const remember=()=>{
    if(encodeMentions(history[historyIndex])===encodeMentions(state))return;
    history=history.slice(0,historyIndex+1);history.push(state);
    if(history.length>200)history.shift();historyIndex=history.length-1;
  };
  const scroll=()=>{mirror.scrollTop=input.scrollTop;mirror.scrollLeft=input.scrollLeft;};
  function paint(){
    mirror.replaceChildren();let offset=0;
    for(const mention of state.mentions){mirror.append(document.createTextNode(state.text.slice(offset,mention.start)),assistantMention(mention));offset=mention.end;}
    mirror.append(document.createTextNode(state.text.slice(offset)+'\n'));
    input.classList.toggle('has-inline-mentions',!!state.mentions.length);mirror.hidden=!state.mentions.length;
    // Match the textarea's actual content width, including a native scrollbar.
    mirror.style.width=input.clientWidth+'px';mirror.style.height=input.clientHeight+'px';scroll();
  }
  function before(event){
    if(event.inputType==='historyUndo'||event.inputType==='historyRedo'){pending={type:event.inputType};return;}
    let start=input.selectionStart,end=input.selectionEnd;
    if(!composing&&!event.isComposing){
      for(const mention of state.mentions){
        const overlaps=start<mention.end&&end>mention.start;
        const backward=start===end&&event.inputType==='deleteContentBackward'&&start===mention.end;
        const forward=start===end&&event.inputType==='deleteContentForward'&&start===mention.start;
        const inside=start===end&&start>mention.start&&start<mention.end;
        if(overlaps||backward||forward||inside){start=Math.min(start,mention.start);end=Math.max(end,mention.end);}
      }
      if(start!==input.selectionStart||end!==input.selectionEnd){
        const replacement=event.inputType.startsWith('delete')?'':event.inputType==='insertLineBreak'||event.inputType==='insertParagraph'?'\n':event.data;
        // WebKit computes its native edit range before beforeinput. Merely
        // changing the selection here would still delete one character of the
        // label. Replace the expanded range as one native undoable operation.
        if(event.cancelable&&typeof replacement==='string'){event.preventDefault();replace(replacement,start,end);return;}
        input.setSelectionRange(start,end);
      }
    }
    pending={start,end,...(pending?.insert?{insert:pending.insert}:{})};
  }
  function change(event){
    const edit=pending;pending=null;
    if(edit?.type==='historyUndo'||edit?.type==='historyRedo'||event.inputType==='historyUndo'||event.inputType==='historyRedo'){
      const direction=(edit?.type||event.inputType)==='historyUndo'?-1:1;
      for(let index=historyIndex+direction;index>=0&&index<history.length;index+=direction){
        if(history[index].text===input.value){historyIndex=index;state=history[index];paint();return;}
      }
    }
    if(input.value!==state.text||edit?.insert){
      state=editMentions(state,input.value,edit?.start!==undefined?edit:undefined);
      if(edit?.insert)state={...state,mentions:[...state.mentions,...edit.insert.mentions.map(mention=>({...mention,start:mention.start+edit.start,end:mention.end+edit.start}))].sort((a,b)=>a.start-b.start)};
      // Pasted serialized references use the same presentation as restored drafts.
      if(!composing&&new RegExp(assistantMentionSource,'u').test(state.text)){
        const caret=input.selectionStart,prefix=decodeMentions(encodeMentions(state,0,caret)).text.length;
        state=decodeMentions(encodeMentions(state));input.value=state.text;input.setSelectionRange(prefix,prefix);
      }
      remember();
    }
    paint();
  }
  function replace(raw,start=input.selectionStart,end=input.selectionEnd){
    for(const mention of state.mentions){
      if(start<mention.end&&end>mention.start||start===end&&start>mention.start&&start<mention.end){start=Math.min(start,mention.start);end=Math.max(end,mention.end);}
    }
    const insert=decodeMentions(raw);input.focus({preventScroll:true});input.setSelectionRange(start,end);
    pending={start,end,insert};
    // Native insertion retains browser undo and selection behavior. Some hosts
    // lack insertText; their fallback still preserves the exact serialized value.
    if(!document.execCommand('insertText',false,insert.text)){
      input.setRangeText(insert.text,start,end,'end');input.dispatchEvent(new Event('input',{bubbles:true}));
    }
  }
  function clipboard(event){
    if(input.selectionStart===input.selectionEnd||!state.mentions.length||!event.clipboardData)return;
    if(event.type==='cut'&&(input.disabled||input.readOnly))return;
    if(event.type==='cut'){
      let start=input.selectionStart,end=input.selectionEnd;
      for(const mention of state.mentions)if(start<mention.end&&end>mention.start){start=Math.min(start,mention.start);end=Math.max(end,mention.end);}
      input.setSelectionRange(start,end);
    }
    event.clipboardData.setData('text/plain',encodeMentions(state,input.selectionStart,input.selectionEnd));event.preventDefault();
    if(event.type==='cut')replace('');
  }
  function paste(event){
    const text=event.clipboardData?.getData('text/plain');
    if(!text||input.disabled||input.readOnly||!new RegExp(assistantMentionSource,'u').test(text))return;
    event.preventDefault();replace(text);
  }
  const start=()=>{composing=true;},end=()=>{composing=false;change({});};
  input.addEventListener('beforeinput',before,true);input.addEventListener('input',change,true);
  input.addEventListener('compositionstart',start,true);input.addEventListener('compositionend',end,true);
  input.addEventListener('scroll',scroll);input.addEventListener('copy',clipboard);input.addEventListener('cut',clipboard);input.addEventListener('paste',paste);
  const observer=new ResizeObserver(paint);observer.observe(input);
  const api={
    value(){if(input.value!==state.text)change({});return encodeMentions(state);},
    replace,
    setValue(raw){state=decodeMentions(raw);pending=null;history=[state];historyIndex=0;setText(state.text);paint();},
    dispose(){observer.disconnect();mirror.remove();editors.delete(input);input.classList.remove('has-inline-mentions');input.removeEventListener('beforeinput',before,true);input.removeEventListener('input',change,true);input.removeEventListener('compositionstart',start,true);input.removeEventListener('compositionend',end,true);input.removeEventListener('scroll',scroll);input.removeEventListener('copy',clipboard);input.removeEventListener('cut',clipboard);input.removeEventListener('paste',paste);},
  };
  editors.set(input,api);setText(state.text);paint();return api;
}
