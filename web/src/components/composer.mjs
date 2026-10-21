import {isMobileBrowser} from './input-platform.mjs';

// Native DOM owns text, selection, paste, undo and IME. Never replace the textarea
// or rewrite its value while composing, including when stream events arrive.
export function createEnterGuard(now = () => performance.now()) {
  let composing = false;
  let endedAt = -Infinity;
  return {
    start() { composing = true; },
    end() { composing = false; endedAt = now(); },
    shouldSend(event) {
      if (event.key !== 'Enter' || event.shiftKey || event.altKey || event.ctrlKey || event.metaKey || event.repeat) return false;
      // Safari can dispatch compositionend before the candidate-confirming
      // Enter, with isComposing false. Its keyCode may also already be 13.
      return !composing && !event.isComposing && event.keyCode !== 229 && now() - endedAt > 80;
    },
    get composing() { return composing; },
  };
}

export function bindComposer(input, {onInput, onSend, onCompositionEnd, layout = work => work(), mobile = isMobileBrowser()}) {
  const guard = createEnterGuard();
  if (mobile) input.setAttribute('enterkeyhint', 'enter');
  let measuredValue, measuredWidth, measuredScrollHeight;
  const resize = () => layout(() => {
    const width = input.clientWidth;
    if (!width || (input.value === measuredValue && width === measuredWidth && input.scrollHeight === measuredScrollHeight)) return;
    input.style.height = 'auto';
    input.style.height = `${Math.min(input.scrollHeight, 150)}px`;
    measuredValue = input.value; measuredWidth = input.clientWidth; measuredScrollHeight = input.scrollHeight;
  });
  const start = () => guard.start();
  const end = () => layout(() => { guard.end(); onInput(input.value); resize(); onCompositionEnd?.(); });
  const change = () => layout(() => { onInput(input.value); resize(); });
  const key = event => {
    if (!mobile && guard.shouldSend(event)) { event.preventDefault(); onSend(); }
  };
  input.addEventListener('compositionstart', start);
  input.addEventListener('compositionend', end);
  input.addEventListener('input', change);
  input.addEventListener('keydown', key);
  return {
    mobile,
    get composing() { return guard.composing; },
    setValue(value) { if (!guard.composing && input.value !== value) { input.value = value; resize(); } },
    resize,
    destroy() {
      input.removeEventListener('compositionstart', start);
      input.removeEventListener('compositionend', end);
      input.removeEventListener('input', change);
      input.removeEventListener('keydown', key);
    },
  };
}
