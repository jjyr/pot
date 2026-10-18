export function modelDefaults(snapshot,providerID,modelID) {
  const provider=(snapshot.directory||[]).find(value=>value.id===providerID);
  const model=(provider?.models||[]).find(value=>value.id===modelID);
  const limits=model||snapshot.model_defaults?.[modelID]||{};
  const context=Number(limits.context)||32768;
  return {context,input:Number(limits.input)||null,output:Math.min(Number(limits.output)||8192,context-1)};
}

// Connection facts are discarded on an address/protocol change, including an
// edit away and back before another discovery completes.
export function createModelInputDiscovery(connection,initial={}) {
  const key=value=>JSON.stringify([value?.endpoint||'',value?.protocol||'']);
  let current=key(connection),inputs=structuredClone(initial);
  return {
    connectionChanged(value){const next=key(value);if(next!==current){current=next;inputs={};}},
    adopt(value,facts){if(key(value)===current)inputs=structuredClone(facts);},
    snapshot(value){return key(value)===current?structuredClone(inputs):{};},
  };
}
