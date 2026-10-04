// Thin Pi adapter; schemas and authorization belong to Runtime.
import { readFileSync } from 'node:fs';
const definitions = JSON.parse(readFileSync(new URL('./pot-tools.json', import.meta.url), 'utf8'));
export default function (pi) {
  for (const {name, description, inputSchema} of definitions) {
    pi.registerTool({
      name, label: name, description,
      parameters: inputSchema,
      async execute(call_id, args, signal) {
        const response = await fetch(process.env.POT_TASK_API, {
          method: "POST",
          headers: { "content-type": "application/json", authorization: `Bearer ${process.env.POT_TASK_TOKEN}` },
          body: JSON.stringify({ name, call_id, args }), signal,
        });
        if (!response.ok) throw new Error(`Task operation rejected (${response.status}): ${await response.text()}`);
        const result = await response.json();
        return { content: [{ type: "text", text: JSON.stringify(result) }], details: {} };
      },
    });
  }
}
