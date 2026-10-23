package protocol

import (
	"strings"
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

func (c Completion) Matches(query string) bool {
	query = strings.ToLower(query)
	return strings.Contains(strings.ToLower(c.Token), query) || strings.Contains(strings.ToLower(c.Label), query) || strings.Contains(strings.ToLower(c.Detail), query)
}
