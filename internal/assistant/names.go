package assistant

import (
	"math/rand/v2"
	"strconv"
	"strings"
)

// These are identity values, deliberately English in every UI language. Pot is
// reserved for first-use bootstrap rather than the manual creation pool.
var kitchenNames = [...]string{"Cup", "Mug", "Bowl", "Pan", "Wok", "Spoon", "Fork", "Tray", "Whisk", "Kettle", "Lid", "Jar", "Jug", "Plate", "Dish", "Glass"}

func randomAssistantName(used []string) string {
	return pickAssistantName(used, rand.IntN)
}

func pickAssistantName(used []string, pick func(int) int) string {
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
