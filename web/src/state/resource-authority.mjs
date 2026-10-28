// Transient provenance for a scoped authoritative read. It travels with query
// data so a retained query cannot be rebound to a newer connection by its view.
export function resourceAuthority(client,scope,resources=client.resources){
  const source=resources?.describe?.('/api/v1/runtime',scope),space=client.spaces?.find(item=>item.id===scope.space);
  return JSON.stringify([client.runtime?.runtime_id,client.runtime?.boot_id,source?.control||space?.control_id,source?.boot||space?.boot_id||client.runtime?.boot_id,client.connectionGeneration,scope.space,scope.owner]);
}
export const resourceAuthorityCurrent=(client,scope,authority,resources=client.resources)=>authority===resourceAuthority(client,scope,resources)&&client.operationScopeReady?.(scope)!==false;
