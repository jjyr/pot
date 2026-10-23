package core

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DownloadFilename converts a label or basename into one portable filename.
// It must not be used to validate workspace paths or archive entries. The 255
// UTF-8 byte limit also fits Windows' 255 UTF-16 code unit component limit.
// Conversion is deterministic, preserves ordinary Unicode and the final
// extension where it fits, and never changes the original display label.
func DownloadFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\<>:"|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, ". ")
	if name == "" {
		return "resource"
	}

	ext := path.Ext(name)
	if ext == name { // A dotfile is already a usable basename.
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	// An extension alone can exceed the component limit. Retain its prefix and
	// reserve at least one ASCII byte for a nonempty stem in that case.
	ext = filenamePrefix(ext, 254)
	budget := 255 - len(ext)
	stem = filenamePrefix(stem, budget)
	if stem == "" {
		stem = filenamePrefix("resource", budget)
	}
	// Windows reserves device names even when followed by one or more suffixes.
	device := strings.ToUpper(strings.TrimRight(strings.SplitN(stem, ".", 2)[0], " "))
	reserved := device == "CON" || device == "PRN" || device == "AUX" || device == "NUL" || device == "CONIN$" || device == "CONOUT$"
	for _, prefix := range []string{"COM", "LPT"} {
		if strings.HasPrefix(device, prefix) {
			suffix := strings.TrimPrefix(device, prefix)
			reserved = reserved || strings.Contains("123456789¹²³", suffix) && utf8.RuneCountInString(suffix) == 1
		}
	}
	if reserved {
		stem = "_" + strings.TrimRight(filenamePrefix(stem, budget-1), ". ")
	}
	result := strings.TrimRight(stem+ext, ". ")
	if result == "" {
		return "resource"
	}
	return result
}

func filenamePrefix(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
