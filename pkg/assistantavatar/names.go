package assistantavatar

import (
	"math/rand/v2"
	"strconv"
	"strings"
)

// Kitchen identities stay English in every UI language.
var kitchenNames = Names()

func RandomName(used []string) string {
	return PickName(used, rand.IntN)
}

func PickName(used []string, pick func(int) int) string {
	taken := make(map[string]bool, len(used))
	for _, name := range used {
		taken[strings.ToLower(strings.TrimSpace(name))] = true
	}
	available := make([]string, 0, len(kitchenNames))
	for _, name := range kitchenNames {
		if !taken[strings.ToLower(name)] {
			available = append(available, name)
		}
	}
	if len(available) > 0 {
		return available[pick(len(available))]
	}
	base := kitchenNames[pick(len(kitchenNames))]
	for number := 2; ; number++ {
		name := base + " " + strconv.Itoa(number)
		if !taken[strings.ToLower(name)] {
			return name
		}
	}
}
