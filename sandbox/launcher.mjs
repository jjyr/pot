// Trusted, isolated SDK adapter. Workload stdio never shares this protocol.
import { SandboxManager, SandboxRuntimeConfigSchema } from '@anthropic-ai/sandbox-runtime';
import { createInterface } from 'node:readline';
import { access } from 'node:fs/promises';
import { constants } from 'node:fs';
import { join } from 'node:path';
const lines = createInterface({ input: process.stdin });
try {
  const first = await lines[Symbol.asyncIterator]().next();
  if (first.done) throw new Error('Missing launch request');
  const request = JSON.parse(first.value);
  process.chdir(request.cwd);
  const literal = path => ({ path, literal: true });
  let seccomp;
  if (process.platform === 'linux') {
    if (!['arm64','x64'].includes(process.arch)) throw new Error('Unsupported sandbox architecture');
    const applyPath = join(import.meta.dirname,'node_modules/@anthropic-ai/sandbox-runtime/vendor/seccomp',process.arch,'apply-seccomp');
    // Upstream treats a missing seccomp helper as a warning. Pot requires Unix
    // socket isolation and must never fall back to a globally installed helper.
    await access(applyPath,constants.X_OK);
    seccomp = { applyPath };
  }
  const config = SandboxRuntimeConfigSchema.parse({
    ...(seccomp ? { seccomp } : {}),
    filesystem: {
      denyRead: request.denyRead.map(literal),
      allowRead: request.read.map(literal),
      allowWrite: request.write.map(literal),
      denyWrite: ['/tmp/claude', '/private/tmp/claude', ...request.denyWrite].map(literal),
    },
    network: { unrestricted: true, allowedDomains: [], deniedDomains: [], allowAllUnixSockets: false },
    allowPty: request.tty === true,
  });
  await SandboxManager.initialize(config, undefined, false);
  const quote = value => "'" + value.replaceAll("'", "'\\''") + "'";
  const launch = await SandboxManager.wrapWithSandboxArgv(request.argv.map(quote).join(' '), '/bin/bash', undefined, undefined, request.cwd);
  process.stdout.write(JSON.stringify({ argv: launch.argv }) + '\n');
  // Retain SDK mount bookkeeping until the executor confirms workload cleanup.
  for await (const line of lines) { if (line === 'close') break; }
} catch (error) {
  process.stdout.write(JSON.stringify({ error: error.message }) + '\n');
  process.exitCode = 1;
} finally {
  await SandboxManager.reset();
  lines.close();
}
