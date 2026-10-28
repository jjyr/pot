export function modelDefaults(snapshot,providerID,modelID) {
  const provider=(snapshot.directory||[]).find(value=>value.id===providerID);
  const model=(provider?.models||[]).find(value=>value.id===modelID);
  const limits=model||snapshot.model_defaults?.[modelID]||{};
  const context=Number(limits.context)||32768;
  return {context,input:Number(limits.input)||null,output:Math.min(Number(limits.output)||8192,context-1)};
}

export function profileDirectoryID(snapshot,profileID) {
  const profile=snapshot.profiles?.find(value=>value.id===profileID);
  const connection=snapshot.connections?.find(value=>value.profile_id===profileID);
  const type=snapshot.provider_types?.find(value=>value.id===connection?.type_id);
  return type?.directory_provider||profile?.model_settings?.catalog_provider||'';
}

// Suggestions are local metadata, never an allowlist or a discovery request.
// Custom API connections without a directory binding can use any public ID.
export function profileModelChoices(snapshot,profileID,selected='') {
  const profile=snapshot.profiles?.find(value=>value.id===profileID);
  if(!profile)return [];
  const directoryID=profileDirectoryID(snapshot,profileID),connection=snapshot.connections?.find(value=>value.profile_id===profileID),models=new Map();
  const unboundCustom=!connection?.type_id||connection.type_id==='standard-model-providers/custom';
  const add=model=>{model=typeof model==='string'?{id:model}:model;if(model?.id)models.set(model.id,model);};
  for(const directory of snapshot.directory||[])if(directoryID?directory.id===directoryID:unboundCustom)for(const model of directory.models||[])add(model);
  for(const model of snapshot.configured_models?.[profileID]||profile.models||[])if(!models.has(typeof model==='string'?model:model.id))add(model);
  for(const model of snapshot.connections?.find(value=>value.profile_id===profileID)?.catalog?.models||[])add(model);
  const choices=providerConversationModels([...models.values()],selected);
  // Owner intent remains selectable even if a refreshed catalog drops the ID.
  for(const id of [profile.model,selected])if(id&&!choices.some(model=>model.id===id))choices.push({id});
  return choices;
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

// Unknown metadata leaves manual configuration available. Known non-conversation
// models are excluded from suggestions, while preserving the current selection.
export function providerConversationModels(entries,selected='') {
  return entries.filter(model=>typeof model==='string'||model.id===selected||
    ((!model.input_modalities?.length||model.input_modalities.includes('text'))&&
     (!model.output_modalities?.length||model.output_modalities.includes('text'))));
}

// Persist only implemented input kinds from the shared typed catalogue.
export function catalogInputModalities(models) {
  return Object.fromEntries(models.filter(model=>model.input_modalities?.length)
    .map(model=>[model.id,model.input_modalities.filter(value=>['text','image','audio','pdf'].includes(value))]));
}
