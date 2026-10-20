// Pot navigation glyphs share a 24 px canvas, rounded terminals and optical insets.
// Keep these separate from compact action icons so the rail and narrow menu agree.
const glyphs = {
  tasks: [
    ['path', {d: 'M8.5 4.5H6A2 2 0 0 0 4 6.5V19a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V6.5a2 2 0 0 0-2-2h-2.5'}],
    ['rect', {x: '8.5', y: '2.5', width: '7', height: '4', rx: '1.5', class: 'navigation-icon-surface'}],
    ['path', {d: 'm7.5 11.5 1.5 1.5 2.5-3M14 12h2.5M7.5 17h9'}],
  ],
  machines: [
    ['path', {d: 'M12 8v4M6 16v-4h12v4'}],
    ['rect', {x: '9', y: '2.5', width: '6', height: '5.5', rx: '1.5', class: 'navigation-icon-surface'}],
    ['rect', {x: '3', y: '16', width: '6', height: '5.5', rx: '1.5', class: 'navigation-icon-surface'}],
    ['rect', {x: '15', y: '16', width: '6', height: '5.5', rx: '1.5', class: 'navigation-icon-surface'}],
  ],
  extensions: [
    ['path', {d: 'M8.5 5.5H5a1.5 1.5 0 0 0-1.5 1.5v4h1a2.5 2.5 0 0 1 0 5h-1v3A1.5 1.5 0 0 0 5 20.5h4v-1a2.5 2.5 0 0 1 5 0v1h3.5A1.5 1.5 0 0 0 19 19v-4h1a2.5 2.5 0 0 0 0-5h-1V7a1.5 1.5 0 0 0-1.5-1.5H14v-1a2.75 2.75 0 0 0-5.5 0Z', class: 'navigation-icon-surface'}],
  ],
  usage: [
    ['rect', {x: '3.5', y: '13', width: '4', height: '7.5', rx: '1.25', class: 'navigation-icon-surface'}],
    ['rect', {x: '10', y: '9', width: '4', height: '11.5', rx: '1.25', class: 'navigation-icon-surface'}],
    ['rect', {x: '16.5', y: '3.5', width: '4', height: '17', rx: '1.25', class: 'navigation-icon-surface'}],
  ],
  settings: [
    ['path', {d: 'M3.5 6h3m5 0h9M3.5 12h10m5 0h2M3.5 18h3m5 0h9'}],
    ['circle', {cx: '9', cy: '6', r: '2.5', class: 'navigation-icon-surface'}],
    ['circle', {cx: '16', cy: '12', r: '2.5', class: 'navigation-icon-surface'}],
    ['circle', {cx: '9', cy: '18', r: '2.5', class: 'navigation-icon-surface'}],
  ],
};

export function navigationIcon(name) {
  if (name === 'chat') {
    const image = document.createElement('img');
    image.className = 'navigation-icon navigation-logo';
    image.src = new URL('../assets/PotCompanion.png', import.meta.url).href;
    image.alt = '';
    image.width = 25;
    image.height = 25;
    return image;
  }
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  for (const [key, value] of Object.entries({class: 'navigation-icon', viewBox: '0 0 24 24', width: '20', height: '20', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.65', 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true', focusable: 'false'})) svg.setAttribute(key, value);
  for (const [tag, attributes] of glyphs[name]) {
    const shape = document.createElementNS(svg.namespaceURI, tag);
    for (const [key, value] of Object.entries(attributes)) shape.setAttribute(key, value);
    svg.append(shape);
  }
  return svg;
}
