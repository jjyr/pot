import {pairing, request} from './client.mjs';
import {createMCPClient} from './mcp-client.mjs';
export function createPlugin({emit}) {
  const client = createMCPClient((body, version) => request(pairing(), '/mcp', {
    method: 'POST', body,
    headers: {Accept: 'application/json, text/event-stream', ...(version ? {'MCP-Protocol-Version': version} : {})},
  }));
  return {
    async execute({tool, input}) {
      const result = await client.callTool(tool, input);
      emit({type: 'text', text: JSON.stringify(result)});
    },
    async dispose() {},
  };
}
