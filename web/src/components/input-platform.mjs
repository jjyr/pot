// Input behavior follows the host/device, never responsive layout or pointer
// media queries: narrow desktop windows and touch laptops remain desktop editors.
export function isMobileBrowser({platform = globalThis.document?.documentElement?.dataset.platform, navigator = globalThis.navigator} = {}) {
  if (platform === 'desktop') return false;
  if (navigator?.userAgentData?.mobile) return true;
  if (/Android|iPhone|iPad|iPod|Mobile/i.test(navigator?.userAgent || '')) return true;
  // iPadOS may identify as macOS when requesting desktop websites. macOS App
  // hosts are excluded above; ordinary Macs do not expose multiple touch points.
  return navigator?.platform === 'MacIntel' && navigator.maxTouchPoints > 1;
}
