// Native IME owns composition. Superseding queries retire their read generation
// in the caller without replacing this input or its focus/selection.
export function bindSearch(input,query,{delay=180}={}) {
  let timer,composing=false;
  const cancel=()=>clearTimeout(timer);
  const flush=()=>{cancel();if(!composing)query(input.value);};
  const schedule=()=>{cancel();if(!composing)timer=setTimeout(flush,delay);};
  const start=()=>{composing=true;cancel();},end=()=>{composing=false;schedule();};
  const key=event=>{if(event.key==='Enter'&&!event.isComposing&&event.keyCode!==229&&!composing)flush();};
  for(const [name,handler]of [['input',schedule],['change',flush],['compositionstart',start],['compositionend',end],['keydown',key]])input.addEventListener(name,handler);
  return ()=>{cancel();for(const [name,handler]of [['input',schedule],['change',flush],['compositionstart',start],['compositionend',end],['keydown',key]])input.removeEventListener(name,handler);};
}
