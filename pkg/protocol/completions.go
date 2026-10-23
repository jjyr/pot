package protocol

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func ValidLabel(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

type Completion struct {
	Token   string `json:"token"`
	Label   string `json:"label"`
	Detail  string `json:"detail"`
	Avatar  string `json:"avatar,omitempty"`
	Offline bool   `json:"offline,omitempty"`
	Local   bool   `json:"local,omitempty"`
}

func CompletionQuery(text string, caret int) (int, string, bool) {
	if caret < 0 || caret > len(text) || !utf8.ValidString(text[:caret]) {
		return 0, "", false
	}
	prefix := text[:caret]
	start := 0
	for i, r := range prefix {
		if unicode.IsSpace(r) {
			start = i + utf8.RuneLen(r)
		}
	}
	word := prefix[start:]
	if len(word) == 0 || word[0] != '/' && word[0] != '@' {
		return start, "", false
	}
	query := word[1:]
	return start, query, !strings.ContainsAny(query, "/@")
}
func (c Completion) Matches(query string) bool {
	query = strings.ToLower(query)
	return strings.Contains(strings.ToLower(c.Token), query) || strings.Contains(strings.ToLower(c.Label), query) || strings.Contains(strings.ToLower(c.Detail), query)
}
