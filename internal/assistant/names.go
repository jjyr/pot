package assistant

import (
	_ "embed"
	"errors"
	"math/rand/v2"
	"sort"
	"strings"
)

// Keep generated identities independent of UI locale and display-name changes.
//
//go:embed names.txt
var assistantWords string

var slugWords = strings.Fields(assistantWords)

func randomAssistantSlug(used []string) (string, error) {
	return pickAssistantSlug(slugWords, used, rand.IntN)
}

// Pick uniformly from the remaining pairs without enumerating the whole space
// or retrying indefinitely when a candidate is already taken.
func pickAssistantSlug(words, used []string, pick func(int) int) (string, error) {
	indices := make(map[string]int, len(words))
	for index, word := range words {
		indices[word] = index
	}
	blocked := map[int]bool{}
	for _, name := range used {
		first, second, ok := strings.Cut(strings.ToLower(strings.Join(strings.Fields(name), "-")), "-")
		if !ok {
			continue
		}
		a, foundA := indices[first]
		b, foundB := indices[second]
		if foundA && foundB {
			blocked[a*len(words)+b] = true
		}
	}
	remaining := len(words)*len(words) - len(blocked)
	if remaining == 0 {
		return "", errors.New("assistant names exhausted")
	}
	occupied := make([]int, 0, len(blocked))
	for index := range blocked {
		occupied = append(occupied, index)
	}
	sort.Ints(occupied)
	index := pick(remaining)
	for _, taken := range occupied {
		if taken > index {
			break
		}
		index++
	}
	return words[index/len(words)] + "-" + words[index%len(words)], nil
}

func slugDisplayName(id string) string {
	parts := strings.Split(id, "-")
	for i, part := range parts {
		if part != "" {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}
