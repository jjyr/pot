// Cached presentation never grants editor write authority. Each section owns
// its refresh so a slow sibling cannot delay an already committed display.
export function createRetainedSection({hydrate,refresh,apply,onState,current=()=>true}) {
  let displayed=false,ready=false,pending=false,error=null,hydrated=false,flight;
  const emit=()=>{if(current())onState({displayed,ready,pending,error});};
  return {
    get displayed(){return displayed;},get ready(){return ready;},
    load(){
      if(flight)return flight;
      pending=true;ready=false;error=null;emit();
      flight=Promise.resolve().then(async()=>{
        if(!hydrated){
          const cached=await hydrate();if(!current())return;hydrated=true;
          if(cached!=null){apply(cached,{fresh:false});displayed=true;emit();}
        }
        const value=await refresh();if(!current())return;
        apply(value,{fresh:true});displayed=true;ready=true;
      }).catch(reason=>{error=reason;ready=false;}).finally(()=>{pending=false;flight=null;emit();});
      return flight;
    },
  };
}

export function mergeEditorProjection(values,{initial,session,controls,touched}) {
  for(const [key,value]of Object.entries(values)){
    if(!touched.has(key)){
      const control=controls.get(key),text=String(value??'');
      if(control.value!==text)control.value=text;
      session.set(key,value);
    }
    initial[key]=value;
  }
  session.acknowledge(values);
}
