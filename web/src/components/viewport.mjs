// The shell and top-layer dialogs share the visible viewport. Keyboard panning
// can change its origin without resizing it; pinch zoom stays browser-owned.
export function bindViewport(target, {host = window, onResize = () => {}} = {}) {
  const viewport = host.visualViewport;
  const properties = ['--viewport-height', '--viewport-width', '--viewport-top', '--viewport-left'];
  const update = () => {
    if (viewport && viewport.scale !== 1) return;
    const values = [viewport?.height ?? host.innerHeight, viewport?.width ?? host.innerWidth,
      viewport?.offsetTop ?? 0, viewport?.offsetLeft ?? 0];
    properties.forEach((name, index) => target.style.setProperty(name, `${values[index]}px`));
    onResize();
  };
  viewport?.addEventListener('resize', update);
  viewport?.addEventListener('scroll', update);
  host.addEventListener('resize', update);
  host.addEventListener('pageshow', update);
  update();
  return () => {
    viewport?.removeEventListener('resize', update);
    viewport?.removeEventListener('scroll', update);
    host.removeEventListener('resize', update);
    host.removeEventListener('pageshow', update);
    properties.forEach(name => target.style.removeProperty(name));
  };
}
