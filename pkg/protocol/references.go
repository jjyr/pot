package protocol

import (
	"fmt"
	"slices"
	"sort"
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
func DefaultLabels(os string) []string { return []string{os} }

type References struct {
	Machine *string
	Tags    []string
	Skills  []string
}

func ParseReferences(text string) (References, error) {
	r := References{}
	for _, token := range strings.Fields(text) {
		if id, ok := strings.CutPrefix(token, "/machine:"); ok {
			if !mailID(id) {
				return r, fmt.Errorf("invalid machine reference")
			}
			if r.Machine != nil && *r.Machine != id {
				return r, fmt.Errorf("select only one machine")
			}
			r.Machine = &id
		} else if tag, ok := strings.CutPrefix(token, "/tag:"); ok {
			if !ValidLabel(tag) {
				return r, fmt.Errorf("invalid machine label")
			}
			r.Tags = append(r.Tags, tag)
		} else if skill, ok := strings.CutPrefix(token, "/skill:"); ok {
			if !ValidLabel(skill) {
				return r, fmt.Errorf("invalid skill reference")
			}
			r.Skills = append(r.Skills, skill)
		}
	}
	sort.Strings(r.Tags)
	r.Tags = slices.Compact(r.Tags)
	sort.Strings(r.Skills)
	r.Skills = slices.Compact(r.Skills)
	if len(r.Tags) > 64 || len(r.Skills) > 128 {
		return r, fmt.Errorf("too many references")
	}
	return r, nil
}

type Completion struct {
	Token  string `json:"token"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
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
	query, ok := strings.CutPrefix(word, "/")
	return start, query, ok && !strings.Contains(query, "/")
}
func (c Completion) Matches(query string) bool {
	query = strings.ToLower(query)
	return strings.Contains(strings.ToLower(c.Token), query) || strings.Contains(strings.ToLower(c.Label), query) || strings.Contains(strings.ToLower(c.Detail), query)
}

type MachineRoute struct {
	RuntimeID string   `json:"runtime_id"`
	Tags      []string `json:"tags"`
}

func SelectMachine(machines []MachineRoute, control string, refs References) (string, error) {
	tags := refs.Tags
	if refs.Machine == nil && len(tags) == 0 {
		for _, machine := range machines {
			if machine.RuntimeID == control {
				return control, nil
			}
		}
		return "", fmt.Errorf("owning Control Runtime is unavailable")
	}
	matches := []string{}
	for _, m := range machines {
		if refs.Machine != nil && m.RuntimeID != *refs.Machine {
			continue
		}
		match := true
		for _, tag := range tags {
			if !slices.Contains(m.Tags, tag) {
				match = false
			}
		}
		if match {
			matches = append(matches, m.RuntimeID)
		}
	}
	if slices.Contains(matches, control) {
		return control, nil
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "", fmt.Errorf("no machine matches selected labels or machine")
	}
	return matches[0], nil
}
