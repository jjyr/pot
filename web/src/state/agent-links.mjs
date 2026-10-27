import {linkDisplayName,linkHostLabel} from './machine-identity.mjs';
// The source reference belongs to the connection; its synthetic directory ID is
// only a UI routing key and is never a remote assistant identity.
export const linkedAssistantID=(linkID,assistantID)=>'agent-link:'+linkID+':'+assistantID;
export function supportsCapability(assistant,operation) {
  return assistant?.capabilities?.version===1&&assistant.capabilities.operations?.includes(operation)===true;
}
export function projectLinkedAssistants(page,space) {
  return (page?.items||[]).flatMap(link=>{
    const directory=link.assistants||link.directory?.targets?.filter(item=>item.address?.kind==='assistant')||[],peer=link.peer||{};
    return directory.map(assistant=>{
      const assistantID=assistant.address?.agent_id||assistant.id;
      if(!assistantID)return null;
      const capabilities=assistant.capabilities;
      if(capabilities&&!supportsCapability({capabilities},'chat.read'))return null;
      return {id:linkedAssistantID(link.id,assistantID),name:assistant.name||assistantID,profile_name:assistant.name,
        avatar:assistant.avatar,reply_id:assistant.reply_id,reply_count:assistant.reply_count||0,last_reply_at_ms:assistant.last_reply_at_ms,preview:assistant.preview,control_id:space.control_id||space.id,space_id:space.id,space_name:'',space_kind:'local',
        offline:link.status!=='connected',phase:link.status==='connected'?'ready':'offline',
        origin:{node_id:peer.node_id,host:linkHostLabel(link),kind:peer.kind||assistant.adapter,name:[linkDisplayName(link),linkHostLabel(link)].filter(Boolean).join(' · '),link_id:link.id,assistant_id:assistantID},
        capabilities:assistant.capabilities||{version:1,operations:[]}};
    }).filter(Boolean);
  });
}
export function agentFailureKey(error) {
  const code=String(error||'');
  for(const [value,key]of Object.entries({
    assistant_busy:'agentLinks.assistantBusy',assistant_changed:'agentLinks.assistantChanged',antigravity_model_unavailable:'agentLinks.modelUnavailable',antigravity_turn_failed:'agentLinks.antigravitySessionFailed',antigravity_invalid_execution_configuration:'agentLinks.antigravitySessionFailed',
    codex_approval_denied:'agentLinks.reviewDenied',
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
    identity_changed:'identityChanged',
  }))if(code.includes(value))return key;
  return code.includes('antigravity_')?'agentLinks.antigravitySessionFailed':null;
}
