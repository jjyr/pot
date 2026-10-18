// Elapsed wall time starts with the Runner's first claim, including later waits.
export function taskElapsed(start,now,locale='en'){
  if(!Number.isSafeInteger(start)||start<=0)return '';
  const seconds=Math.max(0,Math.floor((now-start)/1000)),minutes=Math.floor(seconds/60),hours=Math.floor(minutes/60),days=Math.floor(hours/24),zh=locale==='zh-CN';
  const value=days?(zh?`${days}天${hours%24}小时`:`${days}d ${hours%24}h`):hours?(zh?`${hours}小时${minutes%60}分`:`${hours}h ${minutes%60}m`):minutes?(zh?`${minutes}分${seconds%60}秒`:`${minutes}m ${seconds%60}s`):(zh?`${seconds}秒`:`${seconds}s`);
  return zh?`已用 ${value}`:`Elapsed ${value}`;
}

// One presentation timer per mounted Chat; no projection reads or DOM rebuilds.
export function bindBackgroundTaskClock(root,{locale,active}){
  let timer=0,disposed=false;
  function refresh(){
    clearTimeout(timer);timer=0;
    if(disposed||document.hidden||!active())return;
    const now=Date.now();let delay=Infinity;
    for(const node of root.querySelectorAll('.background-task-elapsed')){
      const start=Number(node.dataset.startedAt),text=taskElapsed(start,now,locale());
      if(node.textContent!==text)node.textContent=text;
      if(!text)continue;
      const elapsed=Math.max(0,now-start),unit=elapsed<3600000?1000:60000;
      delay=Math.min(delay,unit-elapsed%unit);
    }
    if(Number.isFinite(delay))timer=setTimeout(refresh,delay);
  }
  document.addEventListener('visibilitychange',refresh);
  return {refresh,dispose(){disposed=true;clearTimeout(timer);document.removeEventListener('visibilitychange',refresh);}};
}
