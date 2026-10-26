// One owner per stream, including its retry and lifecycle clocks. Native
// EventSource retry is cancelled before our bounded backoff takes ownership.
export const streamDeadlines={initial:20000,idle:45000,stable:15000,maxRetry:30000};
export function createEventStream({url,events,onStatus=()=>{},terminal,host=globalThis,page=globalThis.document,now=()=>Date.now(),random=Math.random,deadlines=streamDeadlines}) {
  let source=null,generation=0,closed=false,opened=false,received=false,started=0,readyAt=0,lastData=0,attempt=0;
  let watchdog,retryTimer,retryAt=now()||1,checking=null,terminalAbort;
  const visible=()=>!page||page.visibilityState!=='hidden';
  const online=()=>host.navigator?.onLine!==false;
  const clear=()=>{host.clearTimeout(watchdog);host.clearTimeout(retryTimer);watchdog=retryTimer=undefined;};
  function stop(){generation++;clear();source?.close();source=null;terminalAbort?.abort();terminalAbort=null;}
  function schedule(){
    host.clearTimeout(watchdog);watchdog=undefined;
    if(closed||!source||!visible())return;
    const due=received?lastData+deadlines.idle:started+deadlines.initial;
    watchdog=host.setTimeout(()=>expired(),Math.max(0,due-now()));
  }
  function retry(code,cause){
    if(closed)return;
    stop();onStatus('offline',{uiKey:'streamDisconnected',code,cause,diagnostic:cause?.message});
    const delay=attempt++===0?0:Math.min(deadlines.maxRetry,500*2**Math.min(attempt-2,6)*(.8+random()*.4));
    retryAt=now()+delay;
    if(visible()&&online())retryTimer=host.setTimeout(connect,Math.max(0,retryAt-now()));
  }
  function expired(){
    if(closed||!source||!visible())return;
    if(now()>=(received?lastData+deadlines.idle:started+deadlines.initial))retry(received?'stream_idle_timeout':opened?'stream_initial_timeout':'stream_headers_timeout');
    else schedule();
  }
  function connect(){
    if(closed||!visible()||!online())return;
    stop();retryAt=0;opened=received=false;started=lastData=now();
    const version=generation,stream=source=new host.EventSource(url);
    const current=()=>!closed&&version===generation&&source===stream;
    onStatus('connecting');schedule();
    stream.onopen=()=>{if(current()){opened=true;schedule();}};
    const alive=()=>{lastData=now();if(received&&lastData-readyAt>=deadlines.stable)attempt=0;schedule();};
    stream.addEventListener('heartbeat',()=>{if(current())alive();});
    for(const [name,accept] of Object.entries(events))stream.addEventListener(name,event=>{
      if(!current())return;
      let deliver;
      try{deliver=accept(event,!received);if(deliver===false)return;}
      catch(reason){retry('stream_invalid_frame',reason);return;}
      if(!current())return;
      if(!received)readyAt=now();received=true;alive();
      // Parsing is a transport responsibility. Consumer/rendering failures must
      // escape to their own error boundary, not cause a network retry loop.
      if(typeof deliver==='function')deliver();
    });
    stream.onerror=()=>{
      if(!current())return;
      if(stream.readyState!==host.EventSource.CLOSED){retry('stream_error');return;}
      // EventSource does not expose HTTP status. A terminal response is checked
      // through the authenticated status route before choosing retry vs auth.
      stop();onStatus('offline',{uiKey:'streamDisconnected',code:'stream_closed'});
      if(terminal){
        const ticket=generation,abort=terminalAbort=new AbortController();
        checking=Promise.resolve().then(()=>terminal(abort.signal)).then(reason=>{
          if(!closed&&ticket===generation&&reason){if(reason.retry)retry('stream_closed');else onStatus('offline',reason);}
        }).catch(()=>{}).finally(()=>{checking=null;if(terminalAbort===abort)terminalAbort=null;});
      }
    };
  }
  function resume(){
    if(closed||!visible())return;
    if(!online()){stop();onStatus('offline',{uiKey:'streamDisconnected',code:'stream_offline'});return;}
    if(source){expired();return;}
    if(checking)return;
    // A terminal CLOSED response waits for an explicit consumer retry/remount.
    if(!retryAt)return;
    host.clearTimeout(retryTimer);retryTimer=host.setTimeout(connect,Math.max(0,retryAt-now()));
  }
  function visibility(){if(!visible()){host.clearTimeout(watchdog);host.clearTimeout(retryTimer);watchdog=retryTimer=undefined;}else resume();}
  function offline(){stop();retryAt=now()||1;onStatus('offline',{uiKey:'streamDisconnected',code:'stream_offline'});}
  for(const name of ['focus','pageshow','online'])host.addEventListener?.(name,resume);
  host.addEventListener?.('offline',offline);page?.addEventListener('visibilitychange',visibility);
  connect();
  return ()=>{closed=true;stop();for(const name of ['focus','pageshow','online'])host.removeEventListener?.(name,resume);host.removeEventListener?.('offline',offline);page?.removeEventListener('visibilitychange',visibility);};
}
