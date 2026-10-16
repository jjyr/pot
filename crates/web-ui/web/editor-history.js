// Browser entries contain route metadata only. Drafts and credentials stay in WASM.
(() => {
  let session = `${Date.now()}-${Math.random()}`;
  let depth = 0, restoring = false, afterRestore, pendingNavigation;
  window.potEditorHistory = {
    get active() { return depth > 0 || restoring; },
    afterRestore(callback) {
      if (restoring) pendingNavigation = callback; else callback();
    },
    setDepth(next) {
      const previous = depth;
      depth = next;
      if (next > 0 && previous === 0) session = `${Date.now()}-${Math.random()}`;
      if (next > previous) {
        for (let step = previous + 1; step <= next; step++) {
          history.pushState({...history.state, potEditor: {session, depth: step}}, '', location.href);
        }
      } else if (next < previous) {
        restoring = true;
        history.go(next - previous);
      }
    },
  };
  addEventListener('popstate', event => {
    if (restoring) {
      event.stopImmediatePropagation();
      restoring = false;
      const callback = afterRestore;
      afterRestore = undefined;
      callback?.();
      const navigation = pendingNavigation;
      pendingNavigation = undefined;
      navigation?.();
      return;
    }
    if (!depth) return;
    const target = event.state?.potEditor?.session === session ? event.state.potEditor.depth : 0;
    if (target >= depth) return;
    event.stopImmediatePropagation();
    // Restore the current entry before asking the shared leave guard. Declining
    // discard therefore leaves both the route and browser history unchanged.
    const levels = depth - target;
    restoring = true;
    afterRestore = () => dispatchEvent(new CustomEvent('pot-editor-back', {detail: levels}));
    history.go(levels);
  });
})();
