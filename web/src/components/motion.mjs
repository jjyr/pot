export const reducedMotion=()=>matchMedia('(prefers-reduced-motion: reduce)').matches;

export async function settleMotion(element,from,to,{duration=220}={}) {
  if(reducedMotion()||!element.isConnected)return;
  const animation=element.animate([from,to],{duration,easing:'cubic-bezier(.2,.8,.2,1)',fill:'both'});
  try{await animation.finished;}catch{/* Disposal or a newer gesture retires it. */}
  animation.cancel();
}

// A stable control acknowledges success in place, including accessible text.
export function copyFeedback(button,{text,locale,translate,onError}) {
  let timer;
  button.onclick=async()=>{
    try{
      await navigator.clipboard.writeText(text());if(!button.isConnected)return;
      clearTimeout(timer);button.dataset.copied='true';
      const label=translate(locale(),'copied');button.title=label;button.setAttribute('aria-label',label);
      timer=setTimeout(()=>{delete button.dataset.copied;const label=translate(locale(),'copy');button.title=label;button.setAttribute('aria-label',label);},1600);
    }catch(error){onError(error);}
  };
}
