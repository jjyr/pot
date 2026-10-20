package plugin

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
)

// Icon reads the optional package-local icon.svg. An invalid/missing asset is
// presentation-only: callers use their default glyph without hiding the package.
// Only self-contained SVG shapes are accepted; URLs and executable markup are not.
func (p Package) Icon() (string, error) {
	data, err := p.ReadFile("icon.svg", 64*1024)
	if err != nil {
		return "", err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	allowed := map[string]bool{"svg": true, "g": true, "path": true, "rect": true, "circle": true, "ellipse": true, "line": true, "polyline": true, "polygon": true, "title": true, "desc": true}
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch v := token.(type) {
		case xml.StartElement:
			if !allowed[v.Name.Local] || v.Name.Space != "" && v.Name.Space != "http://www.w3.org/2000/svg" {
				return "", errors.New("unsupported icon element")
			}
			if depth == 0 {
				if v.Name.Local != "svg" {
					return "", errors.New("invalid icon root")
				}
				roots++
			}
			depth++
			for _, a := range v.Attr {
				switch a.Name.Local {
				case "xmlns", "viewBox", "width", "height", "x", "y", "x1", "x2", "y1", "y2", "cx", "cy", "r", "rx", "ry", "d", "points", "fill", "stroke", "stroke-width", "stroke-linecap", "stroke-linejoin", "opacity", "fill-rule", "clip-rule", "transform":
				default:
					return "", errors.New("unsupported icon attribute")
				}
				if bytes.ContainsAny([]byte(a.Value), "&<") || bytes.Contains(bytes.ToLower([]byte(a.Value)), []byte("url(")) {
					return "", errors.New("external icon reference")
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive, xml.ProcInst:
			return "", errors.New("unsupported icon declaration")
		}
	}
	if roots != 1 {
		return "", errors.New("invalid icon document")
	}
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(data), nil
}
