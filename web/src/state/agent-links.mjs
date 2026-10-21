// The source reference belongs to the connection; its synthetic directory ID is
// only a UI routing key and is never a remote assistant identity.
export const linkedAssistantID=(linkID,assistantID)=>'agent-link:'+linkID+':'+assistantID;
export function supportsCapability(assistant,operation) {
  return assistant?.capabilities?.version===1&&assistant.capabilities.operations?.includes(operation)===true;
}
export function projectLinkedAssistants(page,space) {
  return (page?.items||[]).flatMap(link=>{
    const directory=link.assistants||link.directory?.assistants||[],peer=link.peer||{};
    return directory.map(assistant=>{
      const assistantID=assistant.address?.agent_id||assistant.id;
      if(!assistantID)return null;
      return {id:linkedAssistantID(link.id,assistantID),name:assistant.name||assistantID,
        avatar:assistant.avatar,reply_id:assistant.reply_id,reply_count:assistant.reply_count||0,last_reply_at_ms:assistant.last_reply_at_ms,preview:assistant.preview,control_id:space.control_id||space.id,space_id:space.id,space_name:'',space_kind:'local',
        offline:link.status!=='connected',phase:link.status==='connected'?'ready':'offline',
        origin:{node_id:peer.node_id,kind:peer.kind||assistant.adapter,name:peer.name||peer.node_id,link_id:link.id,assistant_id:assistantID},
        capabilities:assistant.capabilities||link.directory?.operations?.[assistantID]||{version:1,operations:[]}};
    }).filter(Boolean);
  });
}
export function agentFailureKey(error) {
  const code=String(error||'');
  for(const [value,key]of Object.entries({
    codex_approval_requires_local_review:'agentLinks.codexApproval',
    codex_host_restarted_outcome_unknown:'agentLinks.codexOutcomeUnknown',
    codex_cleanup_unconfirmed_requires_local_reconciliation:'agentLinks.codexCleanupUnknown',
    codex_thread_in_use:'agentLinks.codexSessionInUse',
    codex_thread_open_failed:'agentLinks.codexSessionFailed',
    agent_link_capability_unsupported:'agentLinks.capabilityUnavailable',
    agent_link_disconnected:'agentLinks.disconnectedDescription',
    remote_execution_removed:'agentLinks.remoteExecutionRemoved',
    original_execution_unavailable:'agentLinks.originalExecutionUnavailable',
    local_node_required:'agentLinks.localNodeRequired',
  }))if(code.includes(value))return key;
  return null;
}
