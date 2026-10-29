// Detached render inputs with structural sharing. Unlike serialized signatures,
// snapshots retain each unchanged string/subtree once, and exact comparison also
// catches callers that mutate an existing object before their next publication.
export function renderSnapshot(value, previous, seen = new WeakMap()) {
  if(value===null||typeof value!=='object')return Object.is(value,previous)?previous:value;
  if(seen.has(value))return seen.get(value);
  const array=Array.isArray(value),map=value instanceof Map;
  if(!array&&!map&&Object.getPrototypeOf(value)!==Object.prototype&&Object.getPrototypeOf(value)!==null){const clone=structuredClone(value);seen.set(value,clone);return clone;}
  const compatible=previous&&typeof previous==='object'&&(array?Array.isArray(previous):map?previous instanceof Map:Object.getPrototypeOf(previous)===Object.prototype||Object.getPrototypeOf(previous)===null);
  const result=array?[]:map?new Map():{};seen.set(value,result);
  let same=!!compatible;
  if(map){
    same&&=value.size===previous.size;
    const old=compatible?[...previous]:[];let index=0;
    for(const [key,item]of value){
      const prior=old[index++];
      const capturedKey=renderSnapshot(key,prior?.[0],seen),captured=renderSnapshot(item,prior?.[1],seen);
      result.set(capturedKey,captured);same&&=!!prior&&Object.is(capturedKey,prior[0])&&Object.is(captured,prior[1]);
    }
  }else{
    const keys=Object.keys(value);same&&=keys.length===Object.keys(previous).length&&(!array||value.length===previous.length);
    for(const key of keys){
      const captured=renderSnapshot(value[key],compatible?previous[key]:undefined,seen);
      Object.defineProperty(result,key,{value:captured,enumerable:true,writable:true,configurable:true});
      same&&=Object.hasOwn(previous,key)&&Object.is(captured,previous[key]);
    }
    if(array)result.length=value.length;
  }
  const captured=same?previous:result;seen.set(value,captured);return captured;
}

export function createRenderSnapshots(){
  let rows=[],byKey=new Map(),context,revision=0,serial=0;
  const tokens=new WeakMap();
  return {
    capture(next,renderContext){
      const seen=new WeakMap(),nextByKey=new Map();
      const captured=next.map(row=>{const value=renderSnapshot(row,byKey.get(row.viewKey),seen);nextByKey.set(row.viewKey,value);if(!tokens.has(value))tokens.set(value,++serial);return value;});
      if(rows.length!==captured.length||captured.some((row,index)=>row!==rows[index])){rows=captured;revision++;}
      byKey=nextByKey;context=renderSnapshot(renderContext,context,seen);
      return {rows,context,revision};
    },
    token:row=>tokens.get(row),
    clear(){rows=[];byKey.clear();context=undefined;revision++;},
  };
}
