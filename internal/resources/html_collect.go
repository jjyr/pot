package resources

import (
	"bytes"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/jjyr/pot/pkg/protocol"
	"golang.org/x/net/html"
)

var dynamicDependency = regexp.MustCompile(`\b(?:import\s*(?:\(|[\w{*])|fetch\s*\(|XMLHttpRequest\b|WebSocket\s*\()`)

var ErrDependency = errors.New("unsupported HTML dependency")

// CollectHTML reads only static assets under the caller's already-confined root.
// No network, command execution or service discovery is available here.
func CollectHTML(entry string, data []byte, read func(string, int) ([]byte, error)) ([]string, [][]byte, error) {
	paths := []string{entry}
	contents := [][]byte{data}
	seen := map[string]bool{entry: true}
	active := map[string]bool{}
	total := len(data)
	var collect func(string, []byte, int) error
	var asset func(string, string, int) error
	asset = func(reference, base string, depth int) error {
		if reference == "" || strings.HasPrefix(reference, "#") || strings.HasPrefix(reference, "data:") {
			return nil
		}
		u, err := url.Parse(reference)
		if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || strings.HasPrefix(u.Path, "/") {
			return ErrDependency
		}
		name := path.Clean(path.Join(path.Dir(base), u.Path))
		if !protocol.ValidFilePath(name) {
			return ErrDependency
		}
		if active[name] {
			return ErrDependency
		}
		if seen[name] {
			return nil
		}
		if depth > 8 || len(paths) >= 64 {
			return ErrDependency
		}
		seen[name] = true
		raw, err := read(name, BundleLimit-total)
		if err != nil {
			return err
		}
		total += len(raw)
		if total > BundleLimit {
			return ErrDependency
		}
		paths = append(paths, name)
		contents = append(contents, raw)
		return collect(name, raw, depth)
	}
	css := func(base string, raw []byte, depth int) error {
		if bytes.Contains(bytes.ToLower(raw), []byte("@import")) {
			return ErrDependency
		}
		for _, m := range cssURL.FindAllSubmatch(raw, -1) {
			if err := asset(string(m[1]), base, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	collect = func(base string, raw []byte, depth int) error {
		active[base] = true
		defer delete(active, base)
		switch DetectType(base, raw) {
		case "text/css":
			return css(base, raw, depth)
		case "text/javascript":
			if dynamicDependency.Match(raw) {
				return ErrDependency
			}
			return nil
		case "text/html":
			doc, err := html.Parse(bytes.NewReader(raw))
			if err != nil {
				return err
			}
			var walk func(*html.Node) error
			walk = func(n *html.Node) error {
				if n.Type == html.ElementNode {
					if n.Data == "iframe" || n.Data == "object" || n.Data == "embed" {
						return ErrDependency
					}
					if n.Data == "script" && (attribute(n, "type") == "module" || n.FirstChild != nil && dynamicDependency.MatchString(n.FirstChild.Data)) {
						return ErrDependency
					}
					for _, a := range n.Attr {
						if a.Key == "style" {
							if err := css(base, []byte(a.Val), depth); err != nil {
								return err
							}
						}
						if a.Key == "srcset" && a.Val != "" {
							return ErrDependency
						}
						if a.Key == "src" || a.Key == "poster" || a.Key == "href" && n.Data == "link" {
							if err := asset(a.Val, base, depth+1); err != nil {
								return err
							}
						}
					}
					if n.Data == "style" && n.FirstChild != nil {
						if err := css(base, []byte(n.FirstChild.Data), depth); err != nil {
							return err
						}
					}
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					if err := walk(child); err != nil {
						return err
					}
				}
				return nil
			}
			return walk(doc)
		}
		return nil
	}
	if err := collect(entry, data, 0); err != nil {
		return nil, nil, err
	}
	return paths, contents, nil
}
