export function modelDefaults(snapshot,providerID,modelID) {
  const provider=(snapshot.directory||[]).find(value=>value.id===providerID);
  const model=(provider?.models||[]).find(value=>value.id===modelID);
  const limits=model||snapshot.model_defaults?.[modelID]||{};
  const context=Number(limits.context)||32768;
  return {context,input:Number(limits.input)||null,output:Math.min(Number(limits.output)||8192,context-1)};
}
export function providerChoices(snapshot,{all=false,selected=''}={}) {
  const values=(snapshot.directory||[]).filter(value=>all||value.common||value.id===selected).map(value=>[value.id,value.name]);
  if(!all&&(snapshot.directory||[]).some(value=>!value.common))values.push(['__more','More providers…']);
  values.push(['custom','Custom connection']);return values;
}
