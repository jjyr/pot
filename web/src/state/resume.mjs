// Browser focus/visibility events often arrive together. Recover once per burst
// and never overlap recovery reads; hidden pages recover when visible again.
export function bindResumeRecovery(recover,{window:host=window,document:page=document}={}) {
  let timer=null,pending=false,disposed=false;
  const resume=()=>{
    if(disposed||page.visibilityState==='hidden'||timer!==null||pending)return;
    timer=host.setTimeout(async()=>{
      timer=null;if(disposed||page.visibilityState==='hidden')return;
      pending=true;
      try{await recover();}finally{pending=false;}
    },0);
  };
  host.addEventListener('focus',resume);host.addEventListener('pageshow',resume);host.addEventListener('online',resume);page.addEventListener('visibilitychange',resume);
  return ()=>{
    disposed=true;if(timer!==null)host.clearTimeout(timer);
    host.removeEventListener('focus',resume);host.removeEventListener('pageshow',resume);host.removeEventListener('online',resume);page.removeEventListener('visibilitychange',resume);
  };
}
