import {spawn} from 'node:child_process';

export function createPlugin({emit}) {
  return {
    async execute({input}) {
      if (!input.args.length) throw new Error('Supply a gh subcommand');
      const result = await new Promise((resolve, reject) => {
        const child = spawn('gh', input.args, {
          cwd: input.workdir || process.env.POT_EXECUTION_WORKSPACE,
          env: {...process.env, GH_PROMPT_DISABLED: '1', GH_PAGER: 'cat', GIT_TERMINAL_PROMPT: '0'},
          stdio: ['ignore', 'pipe', 'pipe'],
        });
        let output = '', size = 0;
        const append = data => { size += data.length; if (size <= 192 * 1024) output += data.toString(); };
        child.stdout.on('data', append); child.stderr.on('data', append);
        child.on('error', reject);
        child.on('close', (code, signal) => resolve({exit_code: code, signal, output, truncated: size > 192 * 1024}));
      });
      // Avoid accidental echo of the configured token. Deliberate disclosure or
      // transformed values cannot be prevented once a process has the credential.
      result.output = result.output.split(process.env.GH_TOKEN).join('[credential]');
      emit({type: 'text', text: JSON.stringify(result)});
    },
    async dispose() {},
  };
}
