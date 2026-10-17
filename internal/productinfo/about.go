// Package productinfo projects build metadata and bounded plain-text notices.
package productinfo

import (
	"errors"
	"io"
	"os"
	"runtime"
	"unicode/utf8"

	"github.com/jjyr/pot/internal/buildinfo"
)

type Notice struct {
	Name string `json:"name"`
	Text string `json:"text"`
}
type About struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	Revision         string   `json:"revision"`
	GoVersion        string   `json:"go_version"`
	Licenses         []Notice `json:"licenses"`
	NoticesAvailable bool     `json:"notices_available"`
}

// Read never accepts a caller-selected file path. The package resource root and
// fixed notice filenames are the only input, and all content remains plain text.
func Read(directory string) (About, error) {
	out := About{Name: "Pot", Version: buildinfo.Version, Revision: buildinfo.Revision, GoVersion: runtime.Version(), Licenses: []Notice{}}
	root, err := os.OpenRoot(directory)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer root.Close()
	const perFile = 8 * 1024 * 1024
	remaining := 16 * 1024 * 1024
	for _, file := range []struct {
		name, path string
		required   bool
	}{
		{"Pot", "LICENSE.txt", true},
		{"Go dependencies and runtime", "GoDependencyLicenses.txt", true},
		{"Swift Helper dependencies", "SwiftDependencyLicenses.txt", false},
		{"IBM Plex Sans", "FrontendFontLicense.txt", true},
		{"Frontend icons", "FrontendIconLicense.txt", true},
		{"models.dev catalog and artwork", "ModelDirectoryLicense.txt", true},
		{"Node.js and bundled dependencies", "node/LICENSE", true},
		{"noVNC", "novnc/LICENSE.txt", true},
	} {
		info, err := root.Lstat(file.path)
		if os.IsNotExist(err) {
			if file.required {
				out.NoticesAvailable = false
			}
			continue
		}
		if err != nil {
			return out, err
		}
		if !info.Mode().IsRegular() || info.Size() > perFile || info.Size() > int64(remaining) {
			return out, errors.New("invalid or oversized packaged notice")
		}
		f, err := root.Open(file.path)
		if err != nil {
			return out, err
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(min(perFile, remaining))+1))
		f.Close()
		if err != nil {
			return out, err
		}
		if len(data) > perFile || len(data) > remaining || !utf8.Valid(data) {
			return out, errors.New("invalid packaged notice text")
		}
		remaining -= len(data)
		out.Licenses = append(out.Licenses, Notice{Name: file.name, Text: string(data)})
	}
	// Swift notices are platform-specific. The cross-platform notices must
	// all be present before the UI labels a packaged set as available.
	required := map[string]bool{"Pot": false, "Go dependencies and runtime": false, "IBM Plex Sans": false, "Frontend icons": false, "models.dev catalog and artwork": false, "Node.js and bundled dependencies": false, "noVNC": false}
	for _, notice := range out.Licenses {
		if _, ok := required[notice.Name]; ok {
			required[notice.Name] = true
		}
	}
	out.NoticesAvailable = true
	for _, present := range required {
		out.NoticesAvailable = out.NoticesAvailable && present
	}
	return out, nil
}
