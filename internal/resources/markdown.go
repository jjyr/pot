package resources

import (
	"html"
	"regexp"
	"strings"
)

// Link is an explicit Markdown link/image node; offsets refer to original UTF-8
// bytes. Code, raw paths and URL autolinks never become file authority.
type Link struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Href  string `json:"href"`
	Label string `json:"label"`
	Image bool   `json:"image"`
	Block bool   `json:"block"`
}

func escaped(s string, i int) bool {
	n := 0
	for i--; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}
func unescapeLink(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune(`!"#$%&'()*+,-./:;<=>?@[\]^_`+"`"+`{|}~`, rune(s[i+1])) {
			i++
		}
		b.WriteByte(s[i])
	}
	return html.UnescapeString(b.String())
}

var quotePrefix = regexp.MustCompile(`^ {0,3}>[ \t]?`)
var listPrefix = regexp.MustCompile(`^ {0,3}(?:[-+*]|[0-9]+[.)])[ \t]+`)

var linkTitle = regexp.MustCompile(`\s+(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')$`)

func ParseLinks(s string) []Link {
	out := []Link{}
	fence := byte(0)
	fenceLen := 0
	offset := 0
	codeUntil := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		container := line
		for {
			stripped := quotePrefix.ReplaceAllString(container, "")
			if stripped == container {
				break
			}
			container = stripped
		}
		container = listPrefix.ReplaceAllString(container, "")
		trim := strings.TrimLeft(container, " ")
		indent := len(container) - len(trim)
		if codeUntil <= offset && len(trim) >= 3 && (trim[0] == '`' || trim[0] == '~') {
			n := 0
			for n < len(trim) && trim[n] == trim[0] {
				n++
			}
			if n >= 3 && (indent <= 3 || fence != 0) {
				if fence == 0 {
					fence = trim[0]
					fenceLen = n
				} else if fence == trim[0] && n >= fenceLen && strings.TrimSpace(trim[n:]) == "" {
					fence = 0
				}
				offset += len(line)
				continue
			}
		}
		if fence != 0 || len(line)-len(strings.TrimLeft(line, " ")) >= 4 || strings.HasPrefix(line, "\t") {
			offset += len(line)
			continue
		}
		for i := 0; i < len(line); i++ {
			if offset+i < codeUntil || escaped(line, i) {
				continue
			}
			if line[i] == '`' {
				n := 1
				for i+n < len(line) && line[i+n] == '`' {
					n++
				}
				rest := offset + i + n
				for rest < len(s) {
					end := strings.Index(s[rest:], strings.Repeat("`", n))
					if end < 0 {
						break
					}
					at := rest + end
					if (at == 0 || s[at-1] != '`') && (at+n == len(s) || s[at+n] != '`') {
						codeUntil = at + n
						break
					}
					rest = at + n
				}

				continue
			}
			start := i
			image := line[i] == '!' && i+1 < len(line) && line[i+1] == '['
			if image {
				i++
			}
			if line[i] != '[' {
				continue
			}
			labelStart := i + 1
			j := labelStart
			depth := 1
			for ; j < len(line); j++ {
				if escaped(line, j) {
					continue
				}
				if line[j] == '[' {
					depth++
				}
				if line[j] == ']' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if j+1 >= len(line) || line[j+1] != '(' {
				i = start
				continue
			}
			label := line[labelStart:j]
			k := j + 2
			for k < len(line) && (line[k] == ' ' || line[k] == '\t') {
				k++
			}
			begin := k
			href := ""
			if k < len(line) && line[k] == '<' {
				k++
				begin = k
				for k < len(line) && (line[k] != '>' || escaped(line, k)) && line[k] != '\n' {
					k++
				}
				if k >= len(line) || line[k] != '>' {
					i = start
					continue
				}
				href = line[begin:k]
				k++
				for k < len(line) && (line[k] == ' ' || line[k] == '\t') {
					k++
				}
				if k < len(line) && (line[k] == '"' || line[k] == '\'') {
					quote := line[k]
					k++
					for k < len(line) && (line[k] != quote || escaped(line, k)) {
						k++
					}
					if k < len(line) {
						k++
					}
					for k < len(line) && (line[k] == ' ' || line[k] == '\t') {
						k++
					}
				}
				if k >= len(line) || line[k] != ')' {
					i = start
					continue
				}
			} else {
				depth = 1
				for k < len(line) {
					if !escaped(line, k) {
						if line[k] == '(' {
							depth++
						}
						if line[k] == ')' {
							depth--
							if depth == 0 {
								break
							}
						}
					}
					if line[k] == '\n' {
						break
					}
					k++
				}
				if k >= len(line) || depth != 0 {
					i = start
					continue
				}
				href = linkTitle.ReplaceAllString(strings.TrimSpace(line[begin:k]), "")
			}
			if href == "" {
				i = start
				continue
			}
			out = append(out, Link{Start: offset + start, End: offset + k + 1, Href: unescapeLink(href), Label: unescapeLink(label), Image: image, Block: strings.TrimSpace(line[:start]) == "" && strings.TrimSpace(line[k+1:]) == ""})
			i = k
		}
		offset += len(line)
	}
	return out
}
