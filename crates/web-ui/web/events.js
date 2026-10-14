// Bounded SSE decoder; stream subscriptions are cancelled with their owner.
export class Decoder {
  constructor(){this.decoder=new TextDecoder('utf-8',{fatal:true});this.buffer='';this.data='';this.event='message';this.size=0;this.encoder=new TextEncoder();}
  push(bytes){
    const frames=[];this.buffer+=this.decoder.decode(bytes,{stream:true});
    let end;
    while((end=this.buffer.indexOf('\n'))>=0){
      const line=this.buffer.slice(0,end).replace(/\r$/,'');this.buffer=this.buffer.slice(end+1);this.size+=this.encoder.encode(line).length+1;
      if(this.size>8*1024*1024)throw Error('SSE frame exceeds limit');
      if(!line){if(this.data)frames.push({event:this.event,data:this.data.slice(0,-1)});this.data='';this.event='message';this.size=0;}
      else if(!line.startsWith(':')){const split=line.indexOf(':');const field=split<0?line:line.slice(0,split),value=split<0?'':line.slice(split+1).replace(/^ /,'');if(field==='event')this.event=value;if(field==='data')this.data+=value+'\n';}
    }
    if(this.encoder.encode(this.buffer).length+this.size>8*1024*1024)throw Error('SSE frame exceeds limit');
    return frames;
  }
  finish(){this.decoder.decode();if(this.buffer||this.data)throw Error('Incomplete SSE frame');}
}
const wait=(ms,signal)=>new Promise(resolve=>{const done=()=>{clearTimeout(timer);signal.removeEventListener('abort',done);resolve();};const timer=setTimeout(done,ms);signal.addEventListener('abort',done,{once:true});if(signal.aborted)done();});
export function subscribe(url,headers,onFrame,onError){
  const lifetime=new AbortController();let activeReader;
  (async()=>{
    let delay=250;
    while(!lifetime.signal.aborted){
      const attempt=new AbortController(),abort=()=>attempt.abort();lifetime.signal.addEventListener('abort',abort,{once:true});
      let watchdog,reader;
      const touch=()=>{clearTimeout(watchdog);watchdog=setTimeout(abort,20000);};
      try{
        touch();const response=await fetch(url,{headers,credentials:'same-origin',cache:'no-store',signal:attempt.signal});
        if(!response.ok){const error=Error('Subscription rejected: '+response.status);error.status=response.status;throw error;}
        if(!response.headers.get('content-type')?.startsWith('text/event-stream'))throw Error('Expected event stream');
        reader=response.body.getReader();activeReader=reader;const decoder=new Decoder();
        while(!lifetime.signal.aborted){const {value,done}=await reader.read();if(done){decoder.finish();throw Error('Event connection closed');}touch();for(const frame of decoder.push(value)){if(lifetime.signal.aborted)break;onFrame(frame);delay=250;}}
      }catch(error){if(!lifetime.signal.aborted){onError(error);if(error.status===401||error.status===403){lifetime.abort();}}}
      finally{activeReader=undefined;clearTimeout(watchdog);attempt.abort();await reader?.cancel().catch(()=>{});lifetime.signal.removeEventListener('abort',abort);}
      if(!lifetime.signal.aborted){await wait(delay,lifetime.signal);delay=Math.min(delay*2,15000);}
    }
  })();
  return ()=>{lifetime.abort();activeReader?.cancel().catch(()=>{});};
}
