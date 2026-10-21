package assistant

import "github.com/jjyr/pot/pkg/assistantavatar"

var kitchenNames = assistantavatar.Names()

func randomAssistantName(used []string) string { return assistantavatar.RandomName(used) }
func pickAssistantName(used []string, pick func(int) int) string {
	return assistantavatar.PickName(used, pick)
}
