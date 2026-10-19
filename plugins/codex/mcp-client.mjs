// Small sessionless Streamable HTTP client. The transport callback owns TLS,
// credentials, timeouts and response bounds; this module has no Bridge routes.
export function createMCPClient(transport) {
  let nextId = 0, version, initialized;
  const rpc = async (method, params, notification = false) => {
    const id = notification ? undefined : ++nextId;
    const response = await transport({jsonrpc: '2.0', ...(notification ? {} : {id}), method, params}, version);
    if (notification) return;
    if (response?.jsonrpc !== '2.0' || response.id !== id) throw Error('invalid_mcp_response');
    if (response.error) throw Error(`mcp_protocol_error_${response.error.code}`);
    if (!response.result || typeof response.result !== 'object') throw Error('invalid_mcp_result');
    return response.result;
  };
  const initialize = () => initialized ||= (async () => {
    const result = await rpc('initialize', {protocolVersion: '2025-06-18', capabilities: {}, clientInfo: {name: 'pot', version: '0.2.0'}});
    if (!['2025-06-18', '2025-11-25'].includes(result.protocolVersion) || !result.capabilities?.tools) throw Error('unsupported_mcp_server');
    version = result.protocolVersion;
    await rpc('notifications/initialized', {}, true);
  })();
  return {
    async listTools() { await initialize(); return rpc('tools/list', {}); },
    async callTool(name, args) {
      await initialize();
      const result = await rpc('tools/call', {name, arguments: args});
      if (!Array.isArray(result.content)) throw Error('invalid_mcp_tool_result');
      if (result.isError) {
        const text = result.content.filter(item => item.type === 'text').map(item => item.text).join('\n');
        throw Error(text || 'mcp_tool_failed');
      }
      if (result.structuredContent !== undefined) return result.structuredContent;
      const text = result.content.find(item => item.type === 'text')?.text;
      if (typeof text !== 'string') throw Error('invalid_mcp_tool_result');
      try { return JSON.parse(text); } catch { throw Error('invalid_mcp_tool_result'); }
    },
  };
}
