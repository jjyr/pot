import {translate} from '../locales/messages.mjs';

const terminal=new Set(['complete','failed','cancelled','interrupted']);
// Use only the executing node's terminal receipt, never observation/updated time.
export function taskClockEnd(task,now=Date.now()){
  return terminal.has(task.phase)?task.finished_at_ms:now;
}

// Wall time since the Runner's first claim, including later waits.
export function taskElapsed(start,end,locale='en',compact=false){
  if(!Number.isSafeInteger(start)||start<=0||!Number.isSafeInteger(end)||end<=0)return '';
  const seconds=Math.max(0,Math.floor((end-start)/1000)),minutes=Math.floor(seconds/60),hours=Math.floor(minutes/60),days=Math.floor(hours/24);
  const unit=days?'days':hours?'hours':minutes?'minutes':'seconds';
  const major=days||hours||minutes||seconds,minor=days?hours%24:hours?minutes%60:seconds%60;
  const value=translate(locale,`tasks.duration.${unit}`).replace('{major}',major.toLocaleString(locale)).replace('{minor}',minor.toLocaleString(locale));
  return compact?value:translate(locale,'tasks.elapsed').replace('{duration}',value);
}

// One presentation timer per mounted view; no projection reads or DOM rebuilds.
export function bindTaskClock(root,{locale,active,prepare}){
  let timer=0,disposed=false;
  function refresh(){
    clearTimeout(timer);timer=0;
    if(disposed||document.hidden||!active())return;
    const now=Date.now();let delay=Infinity,changed=false;
    for(const node of root.querySelectorAll('[data-started-at]')){
      const start=Number(node.dataset.startedAt),finished=node.hasAttribute('data-finished-at'),end=finished?Number(node.dataset.finishedAt):now;
      const text=taskElapsed(start,end,locale(),node.dataset.compact==='true');
      if(node.textContent!==text){if(prepare)changed=true;else node.textContent=text;}
      if(!text||finished)continue;
      const elapsed=Math.max(0,now-start),unit=elapsed<3600000?1000:60000;
      delay=Math.min(delay,unit-elapsed%unit);
    }
    if(changed)prepare();
    if(Number.isFinite(delay))timer=setTimeout(refresh,delay);
  }
  document.addEventListener('visibilitychange',refresh);
  return {refresh,dispose(){disposed=true;clearTimeout(timer);document.removeEventListener('visibilitychange',refresh);}};
}
