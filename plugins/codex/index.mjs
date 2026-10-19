import {pairing, request} from './client.mjs';
export function createPlugin({emit}) {
  return {
    async execute({tool, input}) {
      const config = pairing();
      const id = value => { if (typeof value !== 'string' || !/^[a-zA-Z0-9_.-]{1,128}$/.test(value)) throw Error('invalid_id'); return value; };
      let result;
      switch (tool) {
        case 'codex_submit': result = await request(config, '/v1/tasks', {method: 'POST', body: input}); break;
        case 'codex_status': result = await request(config, `/v1/tasks/${id(input.task_id)}`); break;
        case 'codex_cancel': result = await request(config, `/v1/tasks/${id(input.task_id)}/cancel`, {method: 'POST', body: {}}); break;
        case 'codex_message': result = await request(config, `/v1/messages/${id(input.message_id)}`); break;
        case 'codex_reply': result = await request(config, `/v1/messages/${id(input.message_id)}/reply`, {method: 'POST', body: {result: input.result}}); break;
        default: throw Error('unknown_codex_tool');
      }
      emit({type: 'text', text: JSON.stringify(result)});
    },
    async dispose() {},
  };
}
