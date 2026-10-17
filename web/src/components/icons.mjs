// Exact retained Lucide glyphs from gpui-kit-assets 0.7.3 (main 6d57b31).
// ISC/Feather MIT notices: web/src/assets/Lucide-LICENSE.txt.
// GitHub mark: primer/octicons icons/mark-github-16.svg (MIT).
// Original notice: web/src/assets/Octicons-LICENSE.txt.
const githubMark = 'M6.766 11.328c-2.063-.25-3.516-1.734-3.516-3.656 0-.781.281-1.625.75-2.188-.203-.515-.172-1.609.063-2.062.625-.078 1.468.25 1.968.703.594-.187 1.219-.281 1.985-.281.765 0 1.39.094 1.953.265.484-.437 1.344-.765 1.969-.687.218.422.25 1.515.046 2.047.5.593.766 1.39.766 2.203 0 1.922-1.453 3.375-3.547 3.64.531.344.89 1.094.89 1.954v1.625c0 .468.391.734.86.547C13.781 14.359 16 11.53 16 8.03 16 3.61 12.406 0 7.984 0 3.563 0 0 3.61 0 8.031a7.88 7.88 0 0 0 5.172 7.422c.422.156.828-.125.828-.547v-1.25c-.219.094-.5.156-.75.156-1.031 0-1.64-.562-2.078-1.609-.172-.422-.36-.672-.719-.719-.187-.015-.25-.093-.25-.187 0-.188.313-.328.625-.328.453 0 .844.281 1.25.86.313.452.64.655 1.031.655s.641-.14 1-.5c.266-.265.47-.5.657-.656';
const navigationGlyphs = {"tasks":[["polyline",{"points":"22 12 16 12 14 15 10 15 8 12 2 12"}],["path",{"d":"M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"}]],"usage":[["path",{"d":"M21 12c.552 0 1.005-.449.95-.998a10 10 0 0 0-8.953-8.951c-.55-.055-.998.398-.998.95v8a1 1 0 0 0 1 1z"}],["path",{"d":"M21.21 15.89A10 10 0 1 1 8 2.83"}]],"settings":[["path",{"d":"M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915"}],["circle",{"cx":"12","cy":"12","r":"3"}]],"machines":[["rect",{"width":"20","height":"14","x":"2","y":"3","rx":"2"}],["line",{"x1":"8","x2":"16","y1":"21","y2":"21"}],["line",{"x1":"12","x2":"12","y1":"17","y2":"21"}]],"menu":[["path",{"d":"M4 5h16"}],["path",{"d":"M4 12h16"}],["path",{"d":"M4 19h16"}]],"chevron-right":[["path",{"d":"m9 18 6-6-6-6"}]]};

navigationGlyphs.cloud=[['path',{d:'M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z'}]];
const paths = {
  menu: 'M4 6h16M4 12h16M4 18h16', back: 'm14 5-7 7 7 7', plus: 'M12 5v14M5 12h14',
  more: 'M5 12h.01M12 12h.01M19 12h.01', send: 'm6 10 6-6 6 6M12 4v16',
  open: 'M14 3h7v7M21 3l-9 9M10 3H3v18h18v-7',
  stop: 'M6 6h12v12H6z', down: 'm6 9 6 6 6-6', close: 'm6 6 12 12M6 18 18 6',
  cloud:'M6 18h12a4 4 0 0 0 0-8h-1a6 6 0 0 0-11 1 3.5 3.5 0 0 0 0 7',
  clock:'M12 8v5h4M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0',
  copy: 'M9 9h11v11H9zM15 5V3H3v12h2',
  alert: 'M12 8v4M12 16h.01M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0',
};
export function icon(name) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  for (const [key, value] of Object.entries({viewBox: '0 0 24 24', width: '20', height: '20', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.7', 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true'})) svg.setAttribute(key, value);
  if(name==='github'){
    svg.setAttribute('viewBox','0 0 16 16');svg.setAttribute('fill','currentColor');svg.setAttribute('stroke','none');
    const path=document.createElementNS(svg.namespaceURI,'path');path.setAttribute('d',githubMark);svg.append(path);
  }else if(navigationGlyphs[name]){
    svg.setAttribute('stroke-width','2');svg.setAttribute('width','16');svg.setAttribute('height','16');
    for(const [tag,attributes]of navigationGlyphs[name]){const shape=document.createElementNS(svg.namespaceURI,tag);for(const [key,value]of Object.entries(attributes))shape.setAttribute(key,value);svg.append(shape);}
  }else{const path=document.createElementNS(svg.namespaceURI,'path');path.setAttribute('d',paths[name]||paths.more);svg.append(path);}
  return svg;
}
