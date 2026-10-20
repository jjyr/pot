package webassets

import "encoding/json"

// Server-authored UI wording shares the frontend catalog. Stored messages are
// rendered once in their creation locale, not translated when read later.
var serverMessages = func() map[string]map[string]string {
	data, err := resources.ReadFile("src/locales/server.json")
	if err != nil {
		panic(err)
	}
	var messages map[string]map[string]string
	if err := json.Unmarshal(data, &messages); err != nil {
		panic(err)
	}
	return messages
}()

func Translate(locale, key string) string {
	if value := serverMessages[locale][key]; value != "" {
		return value
	}
	if value := serverMessages["en"][key]; value != "" {
		return value
	}
	return key
}
