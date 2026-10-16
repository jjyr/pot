// Generic Node plugin transport. Plugin processes use Host process groups or Guest scope cgroups.
import { createInterface } from 'node:readline';
import { pathToFileURL } from 'node:url';
const entry = process.argv[1];
let plugin, currentEmit;
for await (const line of createInterface({ input: process.stdin })) {
  const content = [];
  let bytes = 0;
  try {
    const request = JSON.parse(line);
    const emit = item => {
      if (item.type === 'image') {
        if (request.mode !== 'mixed') throw new Error('Image output unavailable for text models');
        if (!['image/jpeg', 'image/png'].includes(item.mimeType) || typeof item.data !== 'string' || item.data.length > 1400000) throw new Error('Invalid plugin image');
        // Keep only the newest viewport image in a multi-operation REPL invocation.
        const old = content.findIndex(c => c.type === 'image');
        if (old >= 0) content.splice(old, 1);
      } else {
        if (item.type !== 'text' || typeof item.text !== 'string') throw new Error('Invalid plugin content');
        bytes += Buffer.byteLength(item.text);
        if (bytes > 256 * 1024) throw new Error('Plugin output limit exceeded');
      }
      content.push(item);
    };
    // Each execute receives a fresh sink, while plugin state survives calls.
    currentEmit = emit;
    if (!plugin) plugin = (await import(pathToFileURL(entry))).createPlugin({ mode: request.mode, execution_target: request.execution_target, emit: item => currentEmit(item) });
    await plugin.execute({ tool: request.tool, input: request.input });
    process.stdout.write(JSON.stringify({ content }) + '\n');
  } catch (error) {
    process.stdout.write(JSON.stringify({ isError: true, error: `${error.message}; prior effects may have occurred`, content }) + '\n');
  }
}
if (plugin) await plugin.dispose();
