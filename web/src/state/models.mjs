export function modelDefaults(snapshot,providerID,modelID) {
  const provider=(snapshot.directory||[]).find(value=>value.id===providerID);
  const model=(provider?.models||[]).find(value=>value.id===modelID);
  const limits=model||snapshot.model_defaults?.[modelID]||{};
  const context=Number(limits.context)||32768;
  return {context,input:Number(limits.input)||null,output:Math.min(Number(limits.output)||8192,context-1)};
}
