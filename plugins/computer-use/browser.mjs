// Browser backend owned by the computer-use plugin.
import { accessSync, constants, statSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

let chrome, profile, target, session, ownerSession, closing, cleanupError, gracefulExit = false, sequence = 0, nextId = 0, startupDiagnostics = '';
let pending = new Map(), nodes = new Map(), buffer = Buffer.alloc(0);
const MAX_IMAGE = 1024 * 1024;
const KEY_BINDINGS = Object.freeze({ Enter: ['Enter', 13], Tab: ['Tab', 9], Escape: ['Escape', 27], Backspace: ['Backspace', 8], ArrowDown: ['ArrowDown', 40], ArrowUp: ['ArrowUp', 38] });
export const SUPPORTED_KEYS = Object.freeze(Object.keys(KEY_BINDINGS));
export function browserAvailability() {
  const configured = process.env.POT_BROWSER_EXECUTABLE;
  const candidates = configured ? [configured] : [
    '/usr/bin/chromium', '/usr/bin/chromium-browser', '/usr/bin/google-chrome',
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/Applications/Chromium.app/Contents/MacOS/Chromium',
    '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
  ];
  const executable = candidates.find(path => {
    try { accessSync(path, constants.X_OK); return statSync(path).isFile(); }
    catch { return false; }
  });
  if (executable) return {available: true, executable};
  return {available: false, error: `Chromium/Chrome unavailable on this execution target${configured ? ': POT_BROWSER_EXECUTABLE is not an executable file' : ': no executable found at the supported paths'}. Install a supported browser or configure POT_BROWSER_EXECUTABLE on this Runtime.`};
}
function rejectPending(error) {
  for (const request of pending.values()) { clearTimeout(request.timer); request.reject(error); }
  pending.clear();
}
function command(method, params = {}, page = true, timeout = 15000) {
  const child = chrome;
  if (!child || child.exitCode !== null || child.signalCode !== null) return Promise.reject(new Error('Browser session ended; outcome unknown'));
  return new Promise((resolve, reject) => {
    const id = ++nextId;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}; outcome unknown`)); }, timeout);
    pending.set(id, { resolve, reject, timer });
    child.stdio[3].write(JSON.stringify({ id, method, params, ...(page ? { sessionId: session } : {}) }) + '\0', error => {
      if (error && pending.has(id)) { pending.delete(id); clearTimeout(timer); reject(error); }
    });
  });
}
function waitForExit(child, timeout) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve(true);
  return new Promise(resolve => {
    const exited = () => { clearTimeout(timer); resolve(true); };
    const timer = setTimeout(() => { child.removeListener('exit', exited); resolve(false); }, timeout);
    child.once('exit', exited);
  });
}
async function finishClose() {
  if (cleanupError) throw cleanupError;
  const child = chrome, directory = profile;
  rejectPending(new Error('Browser session ended; outcome unknown'));
  if (child?.pid && !gracefulExit && (child.exitCode !== null || child.signalCode !== null)) {
    cleanupError = new Error('Browser exited unexpectedly; descendant cleanup unconfirmed; reset the session; profile retained');
    throw cleanupError;
  }
  if (child?.pid && child.exitCode === null && child.signalCode === null) {
    // Browser.close lets Chrome stop/reap its own helpers before the profile is
    // removed. Keep Chrome in the existing plugin execution scope.
    const exited = waitForExit(child, 2000);
    const graceful = command('Browser.close', {}, false, 1500).catch(() => {});
    if (!await exited) {
      const killed = waitForExit(child, 1000);
      child.kill('SIGKILL');
      const confirmed = await killed;
      await graceful;
      // A main-PID exit cannot prove descendant cleanup after forced shutdown.
      // Retain the profile inside the plugin's private TMPDIR; outer Go scope
      // disposal removes it only after confirmed execution cleanup.
      cleanupError = new Error(confirmed
        ? 'Browser descendant cleanup unconfirmed after forced shutdown; reset the session; profile retained'
        : 'Browser process cleanup unconfirmed after forced shutdown; reset the session; profile retained');
      throw cleanupError;
    }
    await graceful;
    if (child.exitCode !== 0 || child.signalCode !== null) {
      cleanupError = new Error('Browser graceful exit unconfirmed; reset the session; profile retained');
      throw cleanupError;
    }
    gracefulExit = true;
  }
  rejectPending(new Error('Browser session ended; outcome unknown'));
  // Retry only filesystem cleanup, never a browser operation. rm rejects after
  // this bounded retry budget, retaining the profile reference for inspection.
  if (directory) await rm(directory, { recursive: true, force: true, maxRetries: 4, retryDelay: 50 });
  chrome = undefined; profile = undefined; target = undefined;
  nodes.clear(); session = undefined; ownerSession = undefined; buffer = Buffer.alloc(0); gracefulExit = false;
}
async function close() {
  if (closing) return closing;
  closing = finishClose();
  try { await closing; } finally { closing = undefined; }
}
async function open() {
  if (closing) await closing;
  if (chrome || profile) throw new Error('Browser cleanup must complete before opening another session');
  const availability = browserAvailability();
  if (!availability.available) throw new Error(availability.error);
  const executable = availability.executable;
  profile = await mkdtemp(join(tmpdir(), 'pot-browser-'));
  startupDiagnostics = '';
  chrome = spawn(executable, [
    '--headless=new', '--disable-dev-shm-usage', '--no-first-run',
    '--disable-background-networking', '--disable-sync', '--remote-debugging-pipe',
    `--user-data-dir=${profile}`, 'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe', 'pipe', 'pipe'] });
  const child = chrome;
  child.stderr.on('data', chunk => { if (chrome === child) startupDiagnostics = (startupDiagnostics + chunk.toString()).slice(-4096); });
  const ended = error => { if (chrome === child) rejectPending(new Error(`Browser unavailable in this execution environment: ${error.message}${startupDiagnostics ? '\n'+startupDiagnostics : ''}`)); };
  child.on('error', ended);
  child.once('exit', () => ended(new Error('Browser session ended; outcome unknown')));
  child.stdio[3].on('error', ended);
  child.stdio[4].on('error', ended);
  child.stdio[4].on('data', chunk => {
    if (chrome !== child) return;
    buffer = Buffer.concat([buffer, chunk]);
    if (buffer.length > 8 * 1024 * 1024) { child.kill('SIGKILL'); return; }
    let end;
    while ((end = buffer.indexOf(0)) !== -1) {
      const message = JSON.parse(buffer.subarray(0, end).toString()); buffer = buffer.subarray(end + 1);
      const request = pending.get(message.id);
      if (request) {
        pending.delete(message.id); clearTimeout(request.timer);
        if (message.error) request.reject(new Error(message.error.message)); else request.resolve(message.result);
      }
    }
  });
  target = (await command('Target.createTarget', { url: 'about:blank' }, false)).targetId;
  session = (await command('Target.attachToTarget', { targetId: target, flatten: true }, false)).sessionId;
  await command('Page.enable'); await command('DOM.enable'); await command('Accessibility.enable');
  await command('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  ownerSession = randomUUID(); sequence = 0;
}
async function observe(screenshot) {
  sequence++; nodes.clear();
  const tree = (await command('Accessibility.getFullAXTree')).nodes;
  const elements = [];
  for (const node of tree) {
    if (node.ignored) continue;
    const role = node.role?.value, name = String(node.name?.value || '').slice(0, 1000);
    const value = String(node.value?.value || '').slice(0, 1000);
    if (!name && !value && !['textbox', 'button', 'link', 'checkbox', 'combobox', 'slider'].includes(role)) continue;
    const element = String(elements.length + 1);
    if (node.backendDOMNodeId) nodes.set(element, node.backendDOMNodeId);
    elements.push({ element, role, name, value, properties: node.properties?.filter(p => ['checked', 'disabled', 'expanded', 'selected', 'focused'].includes(p.name)) });
    if (elements.length >= 300) break;
  }
  const info = await command('Runtime.evaluate', { expression: 'JSON.stringify({url:location.href,title:document.title,canvas:document.querySelectorAll("canvas").length})', returnByValue: true });
  const page = JSON.parse(info.result.value);
  const content = [{ type: 'text', text: JSON.stringify({ protocol_version: 1, session_id: ownerSession, sequence, viewport: { width: 1280, height: 800 }, ...page, elements, truncated: elements.length === 300, limitations: !screenshot && page.canvas ? 'Canvas content requires visual capability; DOM/AX cannot describe its pixels.' : undefined }) }];
  if (screenshot) {
    const image = await command('Page.captureScreenshot', { format: 'jpeg', quality: 65, captureBeyondViewport: false });
    if (image.data.length > MAX_IMAGE * 4 / 3) throw new Error('Screenshot exceeds image bound');
    content.push({ type: 'image', mimeType: 'image/jpeg', data: image.data, width: 1280, height: 800 });
  }
  return { session_id: ownerSession, sequence, content };
}
async function operation(args) {
  const action = args.action;
  if (action === 'open') {
    if (ownerSession) throw new Error('Browser already open; use its session_id or close it');
    await open();
  } else if (!ownerSession || args.session_id !== ownerSession) throw new Error('Browser session expired or foreign');
  if (action === 'close') { await close(); return { closed: true }; }
  if (action === 'navigate' || (action === 'open' && args.url)) {
    const url = new URL(args.url);
    if (!['http:', 'https:'].includes(url.protocol)) throw new Error('Browser URL requires http or https');
    const reply = await command('Page.navigate', { url: url.href });
    if (reply.errorText) throw new Error(reply.errorText);
    // Wait for the document to settle, bounded; never retry a navigation/action.
    for (let i = 0; i < 100; i++) {
      const ready = await command('Runtime.evaluate', { expression: 'document.readyState', returnByValue: true });
      if (ready.result.value === 'complete') break;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
  } else if (['click', 'fill', 'press', 'scroll'].includes(action)) {
    if (args.sequence !== sequence) throw new Error('Stale observation sequence; observe before acting');
    if (action === 'scroll') {
      await command('Input.dispatchMouseEvent', { type: 'mouseWheel', x: 640, y: 400, deltaX: 0, deltaY: args.delta_y });
    } else if (action === 'click' && args.x !== undefined) {
      if (!args.screenshot) throw new Error('Coordinate action requires visual capability');
      await click(args.x, args.y);
    } else {
      const backendNodeId = nodes.get(args.element);
      if (!backendNodeId) throw new Error('Element unavailable; observe again');
      const object = (await command('DOM.resolveNode', { backendNodeId })).object;
      if (action === 'click') {
        await command('DOM.scrollIntoViewIfNeeded', { backendNodeId });
        const quad = (await command('DOM.getContentQuads', { objectId: object.objectId })).quads[0];
        if (!quad) throw new Error('Element has no visible clickable bounds');
        await click((quad[0] + quad[2] + quad[4] + quad[6]) / 4, (quad[1] + quad[3] + quad[5] + quad[7]) / 4);
      } else if (action === 'fill') {
        const result = await command('Runtime.callFunctionOn', { objectId: object.objectId, functionDeclaration: 'function(value) { if (!(this instanceof HTMLInputElement || this instanceof HTMLTextAreaElement) || this.disabled || this.readOnly) throw new Error("Not an editable text field"); this.focus(); const proto = this instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(proto,"value").set.call(this,value); this.dispatchEvent(new Event("input",{bubbles:true})); this.dispatchEvent(new Event("change",{bubbles:true})); }', arguments: [{ value: args.text }] });
        if (result.exceptionDetails) throw new Error('Field fill failed');
      } else {
        await command('DOM.focus', { backendNodeId });
        const key = typeof args.key === 'string' && Object.hasOwn(KEY_BINDINGS, args.key) && KEY_BINDINGS[args.key];
        if (!key) throw new Error(`Unsupported press key; key must be one of: ${SUPPORTED_KEYS.join(', ')}`);
        await command('Input.dispatchKeyEvent', { type: 'keyDown', key: key[0], code: key[0], windowsVirtualKeyCode: key[1], ...(args.key === 'Enter' ? { text: '\r' } : {}) });
        await command('Input.dispatchKeyEvent', { type: 'keyUp', key: key[0], code: key[0], windowsVirtualKeyCode: key[1] });
      }
    }
  } else if (!['open', 'observe'].includes(action)) throw new Error('Unsupported browser action');
  return observe(args.screenshot === true);
}
async function click(x, y) {
  if (!Number.isFinite(x) || !Number.isFinite(y) || x < 0 || x >= 1280 || y < 0 || y >= 800) throw new Error('Coordinates outside viewport');
  await command('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 });
  await command('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 });
}
export { operation, close };
