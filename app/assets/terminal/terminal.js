'use strict';
const send = body => window.webkit.messageHandlers.potTerminal.postMessage(body);
const terminal = new Terminal({scrollback: 2000, fontSize: 14, fontFamily: 'Menlo, monospace', linkHandler: {activate: (_, url) => { if (/^https?:\/\//i.test(url)) send({action: 'link', url}); }}, theme: {background: '#15171b'}});
const fit = new FitAddon.FitAddon();
terminal.loadAddon(fit);
terminal.open(document.getElementById('terminal'));
terminal.onData(data => send({action: 'input', data}));
terminal.onResize(size => send({action: 'resize', cols: size.cols, rows: size.rows}));
terminal.registerLinkProvider({provideLinks(line, callback) {
  const text = terminal.buffer.active.getLine(line - 1)?.translateToString() || '';
  const links = [...text.matchAll(/https?:\/\/[^\s<>"']+/g)].map(match => ({
    text: match[0], range: {start: {x: match.index + 1, y: line}, end: {x: match.index + match[0].length, y: line}},
    activate: () => send({action: 'link', url: match[0]})
  }));
  callback(links);
}});
window.potTerminal = {
  output(base64) { terminal.write(Uint8Array.from(atob(base64), c => c.charCodeAt(0))); },
  status(text) { terminal.writeln('\r\n[' + text + ']'); },
  reset() { terminal.reset(); },
  focus() { terminal.focus(); fit.fit(); }
};
new ResizeObserver(() => fit.fit()).observe(document.getElementById('terminal'));
fit.fit();
send({action: 'ready'});
