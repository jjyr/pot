// The browser owns editing and IME. GPUI owns the frame, drafts and completion.
export function createComposer({wake, emit}) {
  const input = document.createElement('textarea');
  input.id = 'pot-composer';
  input.hidden = true;
  input.rows = 1;
  input.autocomplete = 'off';
  input.setAttribute('enterkeyhint', 'send');
  document.body.append(input);
  const measure = document.createElement('div');
  measure.id = 'pot-composer-measure';
  measure.setAttribute('aria-hidden', 'true');
  document.body.append(measure);
  const encoder = new TextEncoder();
  let composing = false, committed = false, completion = false, state = {}, geometry;
  let measuredKey, contentHeight;
  const textHeight = () => {
    if (!geometry || input.hidden) return undefined;
    const width = Math.max(1, input.clientWidth - 10);
    const key = JSON.stringify([input.value, width, geometry.font_size, geometry.line_height]);
    if (key !== measuredKey) {
      measuredKey = key;
      Object.assign(measure.style, {width: `${width}px`, fontSize: input.style.fontSize, lineHeight: input.style.lineHeight});
      measure.textContent = input.value + '\u200b';
      contentHeight = measure.getBoundingClientRect().height;
    }
    return contentHeight;
  };
  const snapshot = () => ({
    text: input.value,
    caret: encoder.encode(input.value.slice(0, input.selectionEnd)).length,
    selection: input.selectionStart !== input.selectionEnd,
    composing,
    content_height: textHeight(),
  });
  const focus = () => input.focus({preventScroll: true});
  const send = () => {
    if (composing || committed || input.hidden) return;
    if (state.selected_running && !input.value.trim()) emit({action: 'cancel'});
    else if (state.can_send && input.value.trim()) emit({action: 'send', text: input.value});
    focus();
  };
  input.addEventListener('input', wake);
  input.addEventListener('select', wake);
  input.addEventListener('keyup', wake);
  input.addEventListener('click', wake);
  input.addEventListener('compositionstart', () => { composing = true; wake(); });
  input.addEventListener('compositionend', () => {
    composing = false;
    // Safari can finish composition immediately before the confirming Enter.
    committed = true;
    setTimeout(() => { committed = false; }, 0);
    wake();
  });
  input.addEventListener('keydown', event => {
    if (event.isComposing || composing || event.keyCode === 229 || committed) return;
    const primary = event.metaKey || event.ctrlKey;
    if (primary && ['[', ',', 'd', '.'].includes(event.key)) {
      event.preventDefault();
      emit({action: 'composer-shortcut', key: event.key});
      return;
    }
    if (event.shiftKey || primary || event.altKey) return;
    const key = {ArrowUp: 'up', ArrowDown: 'down', Enter: 'enter', Escape: 'escape'}[event.key];
    if (completion && key) {
      event.preventDefault();
      emit({action: 'composer-key', key});
    } else if (event.key === 'Enter') {
      event.preventDefault();
      send();
    }
  });
  const position = () => {
    if (!geometry) return;
    const canvas = document.querySelector('canvas');
    if (!canvas) return;
    const bounds = canvas.getBoundingClientRect(), g = geometry;
    Object.assign(input.style, {
      left: `${bounds.left + g.x + 8}px`,
      top: `${bounds.top + g.top}px`,
      width: `${Math.max(0, g.width - 8 - g.right)}px`,
      height: `${g.height}px`, padding: `${g.inset_y}px 0 ${g.inset_y}px 10px`,
      fontSize: `${g.font_size}px`, lineHeight: `${g.line_height}px`,
    });
    const previous = contentHeight;
    const next = textHeight();
    if (next !== undefined && next !== previous) wake();
  };
  addEventListener('resize', () => { position(); wake(); });
  visualViewport?.addEventListener('resize', () => { position(); wake(); });
  visualViewport?.addEventListener('scroll', () => { position(); wake(); });
  return {
    snapshot,
    project(value) { state = value; },
    command(value) {
      switch (value.action) {
        case 'editor-visible':
          if (input.hidden && value.value) { input.hidden = false; wake(); }
          else if (!value.value) input.hidden = true;
          break;
        case 'editor-geometry':
          geometry = value;
          input.placeholder = value.placeholder;
          input.setAttribute('aria-label', value.label);
          position();
          break;
        case 'editor-set':
          // Ordinary renders never rewrite the live element: that would reset
          // selection, undo and the IME's composition context.
          if (input.value !== value.text) {
            input.value = value.text;
            input.setSelectionRange(input.value.length, input.value.length);
            wake();
          }
          break;
        case 'editor-replace': {
          const bytes = encoder.encode(input.value), decoder = new TextDecoder();
          const start = decoder.decode(bytes.slice(0, value.start)).length;
          const end = decoder.decode(bytes.slice(0, value.end)).length;
          focus();
          input.setSelectionRange(start, end);
          // A native edit preserves undo for reference completion.
          if (!document.execCommand('insertText', false, value.text)) {
            input.setRangeText(value.text, start, end, 'end');
          }
          wake();
          break;
        }
        case 'completion-active': completion = value.value; break;
        case 'composer-send': send(); break;
      }
    },
  };
}
