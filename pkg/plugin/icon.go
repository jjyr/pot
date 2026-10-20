package plugin

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"image/png"
	"io"
	"os"
)

// Icon reads an optional package-local icon.svg or icon.png. An invalid/missing asset is
// presentation-only: callers use their default glyph without hiding the package.
// Only self-contained SVG shapes are accepted; URLs and executable markup are not.
func (p Package) Icon() (string, error) {
	name, data, err := p.IconAsset()
	if err != nil {
		return "", err
	}
	mime := "image/svg+xml"
	if name == "icon.png" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// IconAsset returns the selected, validated presentation file for package imports.
// SVG takes precedence; an invalid existing SVG does not fall through to PNG.
func (p Package) IconAsset() (string, []byte, error) {
	data, err := p.ReadFile("icon.svg", 64*1024)
	if errors.Is(err, os.ErrNotExist) {
		data, err = p.ReadFile("icon.png", 64*1024)
		if err != nil {
			return "", nil, err
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width > 512 || config.Height > 512 {
			return "", nil, errors.New("invalid PNG icon dimensions")
		}
		if _, err = png.Decode(bytes.NewReader(data)); err != nil {
			return "", nil, err
		}
		return "icon.png", data, nil
	}
	if err != nil {
		return "", nil, err
	}
	if err = validateSVGIcon(data); err != nil {
		return "", nil, err
	}
	return "icon.svg", data, nil
}

func validateSVGIcon(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	allowed := map[string]bool{"svg": true, "g": true, "path": true, "rect": true, "circle": true, "ellipse": true, "line": true, "polyline": true, "polygon": true, "title": true, "desc": true}
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch v := token.(type) {
		case xml.StartElement:
			if !allowed[v.Name.Local] || v.Name.Space != "" && v.Name.Space != "http://www.w3.org/2000/svg" {
				return errors.New("unsupported icon element")
			}
			if depth == 0 {
				if v.Name.Local != "svg" {
					return errors.New("invalid icon root")
				}
				roots++
			}
			depth++
			for _, a := range v.Attr {
				switch a.Name.Local {
				case "xmlns", "viewBox", "width", "height", "x", "y", "x1", "x2", "y1", "y2", "cx", "cy", "r", "rx", "ry", "d", "points", "fill", "stroke", "stroke-width", "stroke-linecap", "stroke-linejoin", "opacity", "fill-rule", "clip-rule", "transform":
				default:
					return errors.New("unsupported icon attribute")
				}
				if bytes.ContainsAny([]byte(a.Value), "&<") || bytes.Contains(bytes.ToLower([]byte(a.Value)), []byte("url(")) {
					return errors.New("external icon reference")
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive, xml.ProcInst:
			return errors.New("unsupported icon declaration")
		}
	}
	if roots != 1 {
		return errors.New("invalid icon document")
	}
	return nil
}
