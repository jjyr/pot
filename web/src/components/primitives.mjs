export function node(tag, className = '', text) {
  const item = document.createElement(tag); if (className) item.className = className;
  if (text !== undefined && text !== null) item.textContent = String(text); return item;
}
export function action(label, callback, {primary = false, danger = false, disabled = false} = {}) {
  const item = node('button', `control${primary ? ' primary' : ''}${danger ? ' danger' : ''}`, label);
  item.type = 'button'; item.disabled = disabled; item.onclick = callback; return item;
}
export function field(label, value = '', {kind = 'text', choices, help = '', required = false, placeholder = '', min, max, readOnly = false} = {}) {
  const row = node('label', 'form-row'), text = node('span', 'form-label', label), body = node('span', 'field-body');
  const input = node(choices ? 'select' : kind === 'textarea' ? 'textarea' : 'input', 'field-input');
  if (choices) for (const [value, label] of choices) { const option = node('option', '', label); option.value = value; input.append(option); }
  else if (kind !== 'textarea') input.type = kind;
  if (kind === 'checkbox') input.checked = Boolean(value); else input.value = value ?? '';
  input.required = required;input.placeholder=placeholder;input.readOnly=readOnly;if(min!==undefined)input.min=min;if(max!==undefined)input.max=max; input.setAttribute('aria-label', label); body.append(input);
  if (help) body.append(node('small', 'hint', help)); row.append(text, body); return {row, input};
}
export function section(title, description) {
  const body = node('section', 'page-section'); body.append(node('h2', '', title));
  if (description) body.append(node('p', 'hint', description)); return body;
}
export function table(headers, rows, keys = []) {
  const wrap = node('div', 'table-scroll'), result = node('table', 'data-table'), head = node('thead'), tr = node('tr');
  wrap.dataset.scrollKey=headers.join('|');for (const label of headers) tr.append(node('th', '', label)); head.append(tr); result.append(head);
  const body = node('tbody'); for (const [index, values] of rows.entries()) { const row = node('tr'); if(keys[index]!=null)row.dataset.viewKey=String(keys[index]); for (const value of values) { const cell = node('td'); cell.append(value instanceof Node ? value : node('span', '', value ?? '—')); row.append(cell); } body.append(row); }
  result.append(body); wrap.append(result); return wrap;
}
export function details(label, value) { const item = node('details', 'detail-disclosure'); item.append(node('summary', '', label), node('pre', '', typeof value === 'string' ? value : JSON.stringify(value, null, 2))); return item; }
