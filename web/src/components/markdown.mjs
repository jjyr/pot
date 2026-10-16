// Preview Markdown subset. All content becomes text nodes; raw HTML is literal.
// The production Markdown renderer and host-controlled links/file delivery remain
// a later migration task. Unsupported Markdown stays readable as source text.
export function allowedLink(value) {
  try { return ['http:', 'https:'].includes(new URL(value).protocol); }
  catch { return false; }
}
function inline(parent, source) {
  const pattern = /(`[^`\n]+`|\*\*[^*\n]+\*\*|\[[^\]\n]+\]\([^\s)]+\))/g;
  let offset = 0;
  for (const match of source.matchAll(pattern)) {
    parent.append(document.createTextNode(source.slice(offset, match.index)));
    const token = match[0];
    if (token.startsWith('`')) {
      const code = document.createElement('code'); code.textContent = token.slice(1, -1); parent.append(code);
    } else if (token.startsWith('**')) {
      const strong = document.createElement('strong'); strong.textContent = token.slice(2, -2); parent.append(strong);
    } else {
      const [, label, href] = token.match(/^\[([^\]]+)\]\((.+)\)$/);
      if (allowedLink(href)) {
        const link = document.createElement('a'); link.href = href; link.textContent = label;
        link.target = '_blank'; link.rel = 'noopener noreferrer'; parent.append(link);
      } else parent.append(document.createTextNode(token));
    }
    offset = match.index + token.length;
  }
  parent.append(document.createTextNode(source.slice(offset)));
}
export function renderMarkdown(source) {
  const result = document.createDocumentFragment();
  const lines = source.split('\n');
  for (let index = 0; index < lines.length;) {
    const line = lines[index];
    if (!line.trim()) { index++; continue; }
    if (/^```/.test(line)) {
      const pre = document.createElement('pre');
      const code = document.createElement('code');
      const body = [];
      for (index++; index < lines.length && !/^```/.test(lines[index]); index++) body.push(lines[index]);
      index++; code.textContent = body.join('\n'); pre.append(code); result.append(pre); continue;
    }
    const heading = line.match(/^(#{1,6})\s+(.+)$/);
    if (heading) {
      const element = document.createElement(`h${heading[1].length}`); inline(element, heading[2]); result.append(element); index++; continue;
    }
    if (/^[-*] /.test(line)) {
      const list = document.createElement('ul');
      while (index < lines.length && /^[-*] /.test(lines[index])) {
        const item = document.createElement('li'); inline(item, lines[index++].slice(2)); list.append(item);
      }
      result.append(list); continue;
    }
    const paragraph = document.createElement('p');
    const body = [line];
    for (index++; index < lines.length && lines[index].trim() && !/^(#{1,6}\s|```|[-*] )/.test(lines[index]); index++) body.push(lines[index]);
    inline(paragraph, body.join('\n')); result.append(paragraph);
  }
  return result;
}
