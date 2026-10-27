package resources

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// A separate HTTP document avoids inheriting the application's script-src
// policy. Its response must carry HTMLPolicy, including the origin sandbox.
const HTMLPolicy = "default-src 'none'; script-src 'unsafe-inline' data:; style-src 'unsafe-inline' data:; img-src data:; media-src data:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; sandbox allow-scripts"

var cssURL = regexp.MustCompile(`url\(\s*['"]?([^'"\)]+)['"]?\s*\)`)

//go:embed html_scroll.js
var htmlScrollBoundary string

func RenderHTML(resource Resource, read func(string) ([]byte, error), token string) ([]byte, error) {
	if resource.MIMEType != "text/html" || resource.Size > BundleLimit || len(resource.Files) > 64 || len(token) > 128 {
		return nil, errors.New("invalid HTML resource")
	}
	original, err := read(resource.Entry)
	if err != nil {
		return nil, err
	}
	document, err := html.Parse(bytes.NewReader(original))
	if err != nil {
		return nil, err
	}
	files := map[string]File{}
	for _, file := range resource.Files {
		files[file.Path] = file
	}
	var total uint64
	var asset func(string, string, int) (string, error)
	asset = func(reference, base string, depth int) (string, error) {
		if strings.HasPrefix(reference, "#") || strings.HasPrefix(reference, "data:") {
			return reference, nil
		}
		parsed, err := url.Parse(reference)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || depth > 8 {
			return "", nil
		}
		name := path.Clean(path.Join(path.Dir(base), parsed.Path))
		if strings.HasPrefix(parsed.Path, "/") {
			name = strings.TrimPrefix(parsed.Path, "/")
		}
		file, ok := files[name]
		if !ok || name == resource.Entry {
			return "", nil
		}
		raw, err := read(name)
		if err != nil {
			return "", err
		}
		total += uint64(len(raw))
		if total > 4*BundleLimit {
			return "", errors.New("HTML asset expansion limit")
		}
		if file.MIMEType == "text/css" {
			var replaceErr error
			raw = cssURL.ReplaceAllFunc(raw, func(match []byte) []byte {
				parts := cssURL.FindSubmatch(match)
				value, err := asset(string(parts[1]), name, depth+1)
				if err != nil {
					replaceErr = err
				}
				return []byte(`url("` + value + `")`)
			})
			if replaceErr != nil {
				return "", replaceErr
			}
		}
		suffix := ""
		if parsed.Fragment != "" {
			suffix = "#" + parsed.EscapedFragment()
		}
		return "data:" + file.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(raw) + suffix, nil
	}
	var head *html.Node
	var walk func(*html.Node) error
	walk = func(n *html.Node) error {
		if n.Type == html.ElementNode {
			if n.Data == "head" {
				head = n
			}
			if n.Data == "base" || n.Data == "meta" && attribute(n, "http-equiv") != "" {
				if n.Parent != nil {
					n.Parent.RemoveChild(n)
				}
				return nil
			}
			for index := range n.Attr {
				attr := &n.Attr[index]
				switch attr.Key {
				case "src", "href", "poster":
					value, err := asset(attr.Val, resource.Entry, 0)
					if err != nil {
						return err
					}
					attr.Val = value
				case "srcset":
					attr.Val = ""
				case "style":
					var replaceErr error
					attr.Val = cssURL.ReplaceAllStringFunc(attr.Val, func(match string) string {
						parts := cssURL.FindStringSubmatch(match)
						value, err := asset(parts[1], resource.Entry, 0)
						replaceErr = err
						return `url("` + value + `")`
					})
					if replaceErr != nil {
						return replaceErr
					}
				}
			}
			if n.Data == "style" && n.FirstChild != nil {
				var replaceErr error
				n.FirstChild.Data = cssURL.ReplaceAllStringFunc(n.FirstChild.Data, func(match string) string {
					parts := cssURL.FindStringSubmatch(match)
					value, err := asset(parts[1], resource.Entry, 0)
					if err != nil {
						replaceErr = err
					}
					return `url("` + value + `")`
				})
				if replaceErr != nil {
					return replaceErr
				}
			}
		}
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			if err := walk(child); err != nil {
				return err
			}
			child = next
		}
		return nil
	}
	if err := walk(document); err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(token)
	script := &html.Node{Type: html.ElementNode, Data: "script"}
	script.AppendChild(&html.Node{Type: html.TextNode, Data: `document.addEventListener('securitypolicyviolation',()=>parent.postMessage({type:'pot-resource',token:` + string(encoded) + `,action:'dependency_error'},'*'));Object.defineProperty(window,'pot',{value:Object.freeze({resize:value=>parent.postMessage({type:'pot-resource',token:` + string(encoded) + `,action:'resize',value},'*'),result:value=>parent.postMessage({type:'pot-resource',token:` + string(encoded) + `,action:'result',value},'*')})});parent.postMessage({type:'pot-resource',token:` + string(encoded) + `,action:'ready'},'*');`})
	// Keyboard events do not bubble across the opaque-origin frame. Forward only
	// an unhandled real Escape; the host can close this frame's own modal viewer.
	script.AppendChild(&html.Node{Type: html.TextNode, Data: `document.addEventListener('keydown',event=>{if(event.key==='Escape'&&event.isTrusted)setTimeout(()=>{if(!event.defaultPrevented)parent.postMessage({type:'pot-resource',token:` + string(encoded) + `,action:'escape'},'*');});});`})
	if head != nil {
		head.InsertBefore(script, head.FirstChild)
		boundary := &html.Node{Type: html.ElementNode, Data: "script"}
		boundary.AppendChild(&html.Node{Type: html.TextNode, Data: htmlScrollBoundary})
		head.InsertBefore(boundary, script)
	}
	var out bytes.Buffer
	if err := html.Render(&out, document); err != nil {
		return nil, err
	}
	if out.Len() > 4*BundleLimit {
		return nil, errors.New("HTML output limit")
	}
	return out.Bytes(), nil
}
func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}
