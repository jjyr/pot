package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type HostMapping struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"read_only"`
}

func ValidateMappings(mappings []HostMapping) error {
	if len(mappings) > 16 {
		return errors.New("Host mapping limit reached")
	}
	destinations := []string{}
	for _, mapping := range mappings {
		if !filepath.IsAbs(mapping.Source) || strings.ContainsAny(mapping.Source, ":,\n\x00") {
			return errors.New("unsupported Host mapping source")
		}
		canonical, err := filepath.EvalSymlinks(mapping.Source)
		if err != nil || canonical != mapping.Source {
			return errors.New("Host mapping source must remain a canonical directory")
		}
		info, err := os.Stat(mapping.Source)
		if err != nil || !info.IsDir() {
			return errors.New("Host mapping source is not a directory")
		}
		target := mapping.Destination
		if !strings.HasPrefix(target, "/mnt/host/") || filepath.Clean(target) != target || strings.ContainsAny(target, ":,\n\x00") {
			return errors.New("Host mapping destination must be beneath /mnt/host")
		}
		for _, existing := range destinations {
			if target == existing || strings.HasPrefix(target, existing+"/") || strings.HasPrefix(existing, target+"/") {
				return errors.New("overlapping Host mapping destinations")
			}
		}
		destinations = append(destinations, target)
	}
	return nil
}
