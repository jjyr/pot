package executor

import (
	"os"
	"path/filepath"
)

// patchWriteScope describes executor-owned grants for strategy selection only.
// It never grants access: every write still runs under the prepared OS policy.
// Unknown/nonexistent roots cannot select in-place writes.
type patchWriteScope struct {
	Directories []string `json:"directories"`
	Files       []string `json:"files"`
}

func patchWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

func (s patchWriteScope) directoryCovers(path string) bool {
	for _, directory := range s.Directories {
		if patchWithin(path, directory) {
			return true
		}
	}
	return false
}

// patchScope snapshots the same write roots used to launch this patch worker.
// Linux binds each allowWrite entry, including redundant file entries. Omit a
// file entry already covered by a directory grant so it cannot obstruct atomic
// replacement. This adds no paths to the OS allowlist.
func patchScope(workspace string, write []string) (patchWriteScope, []string) {
	var scope patchWriteScope
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
			scope.Directories = append(scope.Directories, canonical)
		} else if info.Mode().IsRegular() {
			files[i] = canonical
		}
	}
	filtered := make([]string, 0, len(write))
	for i, root := range roots {
		if file, ok := files[i]; ok {
			if scope.directoryCovers(file) {
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

func (s patchWriteScope) inPlace(edit patchEdit) bool {
	if edit.original == nil || edit.content == nil || edit.destination != "" {
		return false
	}
	canonical, err := filepath.EvalSymlinks(edit.path)
	if err != nil || s.directoryCovers(canonical) {
		return false
	}
	for _, file := range s.Files {
		if canonical == file {
			return true
		}
	}
	return false
}
