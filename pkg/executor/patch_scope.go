package executor

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

// patchWriteScope carries uncovered exact-file grants; OS policy enforces access.
type patchWriteScope struct {
	Files []string `json:"files"`
}

// patchScope removes redundant Linux file binds so directories retain atomic writes.
func patchScope(workspace string, write []string) (*patchWriteScope, []string) {
	if runtime.GOOS != "linux" {
		return nil, write
	}
	scope := &patchWriteScope{}
	var directories []string
	roots := append([]string{workspace}, write...)
	files := make(map[int]string)
	for i, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		info, err := os.Stat(canonical)
		if err != nil {
			continue
		}
		if info.IsDir() {
			directories = append(directories, canonical)
		} else if info.Mode().IsRegular() {
			files[i] = canonical
		}
	}
	covered := func(path string) bool {
		for _, directory := range directories {
			rel, err := filepath.Rel(directory, path)
			if err == nil && filepath.IsLocal(rel) {
				return true
			}
		}
		return false
	}
	filtered := make([]string, 0, len(write))
	for i, root := range roots {
		if file, ok := files[i]; ok {
			if covered(file) {
				continue
			}
			scope.Files = append(scope.Files, file)
		}
		if i > 0 {
			filtered = append(filtered, root)
		}
	}
	return scope, filtered
}

func (s *patchWriteScope) inPlace(edit patchEdit) bool {
	if s == nil || len(s.Files) == 0 || edit.original == nil || edit.content == nil || edit.destination != "" {
		return false
	}
	canonical, err := filepath.EvalSymlinks(edit.path)
	return err == nil && slices.Contains(s.Files, canonical)
}
