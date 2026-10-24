import {communication} from './communication.mjs';
import {addressKey} from './task-identity.mjs';

// A receive batch is indivisible. Mixed mail, incomplete calls and every other
// transcript item are boundaries, even if they disappear from a virtual window.
export function stackableCommunication(message, actor, blocked = () => false) {
  const tool = message?.tool;
  if (message.role !== 'tool' || !['send_message', 'receive_message'].includes(tool?.name)
    || tool.phase !== 'completed' || tool.script || tool.diagnostic || tool.failurePhase || blocked(message)) return null;
  const model = communication(tool, actor);
  return model?.messages.length && model.messages.every(mail => mail.text !== null
    && [mail.from, mail.to].every(address => addressKey(address))
    && [mail.from, mail.to].some(address => address.kind === 'task')) ? model : null;
}

// Retain an existing group's identity when history is prepended or groups join.
// Membership is computed over the logical siblings, before virtualization.
export function createCommunicationProjection() {
  let previous = new Map();
  return (messages, {actor, blocked, anchor} = {}) => {
    const result = [], next = new Map(), used = new Set();
    const anchoredGroup=previous.get(messages.find(message=>message.id===anchor)?.viewKey);
    let run = [];
    const flush = () => {
      if (!run.length) return;
      if (run.length === 1) result.push(run[0]);
      else {
        const retained = run.find(message => message.id === anchor && previous.has(message.viewKey) && !used.has(previous.get(message.viewKey)))
          || run.find(message => previous.has(message.viewKey) && !used.has(previous.get(message.viewKey)) && (previous.get(message.viewKey)!==anchoredGroup||run.some(member=>member.id===anchor)));
        let key = previous.get(retained?.viewKey) || `communication:${run[0].viewKey}`;
        if(!retained&&key===anchoredGroup&&!run.some(member=>member.id===anchor))key+=':split';
        while(used.has(key))key+=':split';
        used.add(key);
        const group = {id:key, viewKey:key, role:'tool', members:run};
        result.push(group);
        for (const message of run) next.set(message.viewKey, key);
      }
      run = [];
    };
    for (const message of messages) {
      if (stackableCommunication(message, actor, blocked)) run.push(message);
      else { flush(); result.push(message); }
    }
    flush(); previous = next;
    return result;
  };
}

export function communicationCounts(members, actor) {
  const tasks = new Set(), routes = new Map(); let messages = 0;
  for (const record of members) for (const mail of communication(record.tool, actor).messages) {
    messages++;
    for (const address of [mail.from, mail.to]) if (address.kind === 'task') tasks.add(addressKey(address));
    const key = JSON.stringify([addressKey(mail.from), addressKey(mail.to)]);
    if (!routes.has(key)) routes.set(key, {mail, count:0});
    routes.get(key).count++;
  }
  return {records:members.length, messages, tasks:tasks.size, routes:[...routes.values()]};
}
