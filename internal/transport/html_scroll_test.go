package transport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic content uses only the existing, unprivileged local result bridge.
func writeHTMLScrollFixtures(t *testing.T, workspace string) {
	t.Helper()
	for _, kind := range []string{"long", "nested", "short"} {
		content := `<p>Short interactive document.</p>`
		if kind != "short" {
			content = `<section id="list" tabindex="0">` + strings.Repeat(`<p>Sample row · native scrolling and selection</p>`, 45) + `</section>`
		}
		style := ""
		if kind == "nested" {
			style = "#list{height:180px;overflow:auto;border:1px solid #bbb}"
		}
		surface := "document.scrollingElement"
		if kind == "nested" {
			surface = "list"
		}
		source := `<!doctype html><html><head><style>body{margin:0;padding:16px;font:14px system-ui;background:#f3f7f1;color:#294b3d}header{position:sticky;top:0;background:#f3f7f1;padding:8px 0}button,input{margin:4px;padding:6px}p{padding:5px} ` + style + `</style></head><body><header><strong>` + kind + ` HTML</strong><button id="top">Top</button><button id="bottom">Bottom</button><button id="action">Interact</button><input aria-label="Editable text" value="Keep editing"></header>` + content + `<script>
const list=document.querySelector('#list'),surface=` + surface + `;
let count=0;const report=()=>pot.result({kind:'` + kind + `',root:document.scrollingElement.scrollTop,nested:list?.scrollTop||0,max:surface.scrollHeight-surface.clientHeight,interactions:count,isolated:(()=>{try{return !parent.document}catch{return true}})()});
document.querySelector('#top').onclick=()=>{surface.scrollTop=0;report()};document.querySelector('#bottom').onclick=()=>{surface.scrollTop=surface.scrollHeight;report()};document.querySelector('#action').onclick=()=>{count++;report()};
document.addEventListener('scroll',report,true);addEventListener('load',report);</script></body></html>`
		if err := os.WriteFile(filepath.Join(workspace, "scroll-"+kind+".html"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
