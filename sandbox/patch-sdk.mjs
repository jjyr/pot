// Pinned SDK extension: unrestricted IP networking with Unix IPC still denied.
// Keep the public schema explicit; never rely on omitted/invalid config values.
import { readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
const root = process.argv[2];
const patch = (file, expected, changes) => {
  const path = join(root, 'node_modules/@anthropic-ai/sandbox-runtime/dist/sandbox', file);
  let source = readFileSync(path, 'utf8');
  if (createHash('sha256').update(source).digest('hex') !== expected) throw new Error(`SRT source drift: ${file}`);
  for (const [before, after, count = 1] of changes) {
    if (source.split(before).length - 1 !== count) throw new Error(`SRT patch context: ${file}`);
    source = source.split(before).join(after);
  }
  writeFileSync(path, source);
};
patch('sandbox-config.js', '76c920d53def7d518697147fc2a475f4614b511af8a6a480a35218638a46fe7d', [
  ['export const NetworkConfigSchema = z.object({', 'export const NetworkConfigSchema = z.object({\n    unrestricted: z.boolean().optional(),'],
]);
patch('sandbox-manager.js', '9fc99f9affab4035e360812c5c9f0e980227642fb31c77588ed8238e59dd914e', [
  ['    // Initialize network infrastructure', '    if (runtimeConfig.network.unrestricted === true) return;\n    // Initialize network infrastructure'],
  ['const hasNetworkConfig = customConfig?.network?.allowedDomains !== undefined ||\n        config?.network?.allowedDomains !== undefined;', 'const hasNetworkConfig = config?.network?.unrestricted !== true && (customConfig?.network?.allowedDomains !== undefined ||\n        config?.network?.allowedDomains !== undefined);'],
  ['const hasNetworkConfig = customConfig?.network?.allowedDomains !== undefined ||\n            config?.network?.allowedDomains !== undefined;', 'const hasNetworkConfig = config?.network?.unrestricted !== true && (customConfig?.network?.allowedDomains !== undefined ||\n            config?.network?.allowedDomains !== undefined);'],
]);
patch('macos-sandbox-utils.js', '7f297d6acb08a44adc9198876a38ce49ffa59e3a7aa09692933b3e008b446a32', [
  ["profile.push('(allow network*)');", "profile.push('(allow network-outbound (remote ip \"*:*\"))');\n        profile.push('(allow network-inbound (local ip \"*:*\"))');\n        profile.push('(allow network-bind (local ip \"*:*\"))');\n        // DNS resolution uses this system socket; no workload-controlled socket.\n        profile.push('(allow network-outbound (literal \"/private/var/run/mDNSResponder\"))');"],
]);
