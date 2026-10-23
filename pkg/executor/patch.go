package executor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const patchLimit = 4 * 1024 * 1024

type patchEdit struct {
	path, destination string
	content           *string
	mode              os.FileMode
	original          *patchOriginal
}

type patchOriginal struct {
	file, parent os.FileInfo
	data         []byte
}

func patchPath(root, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) {
		return "", errors.New("empty or invalid patch path")
	}
	target := name
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	if target == filepath.VolumeName(target)+string(filepath.Separator) {
		return "", errors.New("cannot patch filesystem root")
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("cannot patch a symlink")
	}
	existing := target
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", errors.New("invalid patch ancestor")
		}
		existing = parent
	}
	if _, err := filepath.EvalSymlinks(existing); err != nil {
		return "", err
	}
	return target, nil
}
func textLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.TrimSuffix(text, "\n")
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

// ApplyPatch validates all paths and hunks before the first mutation. Each write
// is fsynced. Directory write permission permits atomic replacement; a file-only
// grant permits a verified in-place write, which is not crash-atomic.
// Multi-file patches are not transactions and
// OS errors during commit must be reported as partial effects.
func ApplyPatch(workspace, patch string) (string, error) {
	if len(patch) > patchLimit || !utf8.ValidString(patch) {
		return "", errors.New("patch exceeds 4 MiB or invalid UTF-8")
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	lines := textLines(patch)
	if len(lines) < 2 || lines[0] != "*** Begin Patch" || lines[len(lines)-1] != "*** End Patch" {
		return "", errors.New("patch requires Begin/End markers")
	}
	edits := []patchEdit{}
	index := 1
	for index < len(lines)-1 {
		header := lines[index]
		index++
		kind, name, ok := strings.Cut(header, ": ")
		if !ok {
			return "", fmt.Errorf("invalid patch header: %s", header)
		}
		target, err := patchPath(root, name)
		if err != nil {
			return "", err
		}
		edit := patchEdit{path: target, mode: 0644}
		switch kind {
		case "*** Add File":
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				return "", errors.New("add target already exists")
			}
			var body strings.Builder
			for index < len(lines) && !strings.HasPrefix(lines[index], "*** ") {
				if !strings.HasPrefix(lines[index], "+") {
					return "", errors.New("added lines require +")
				}
				body.WriteString(lines[index][1:])
				body.WriteByte('\n')
				index++
			}
			value := body.String()
			edit.content = &value
		case "*** Delete File":
			info, err := os.Stat(target)
			if err != nil || !info.Mode().IsRegular() {
				return "", errors.New("delete target missing")
			}
		case "*** Update File":
			info, err := os.Stat(target)
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() || info.Size() > patchLimit {
				return "", errors.New("file too large or not regular")
			}
			edit.mode = info.Mode().Perm()
			original, err := os.ReadFile(target)
			if err != nil {
				return "", err
			}
			if !utf8.Valid(original) {
				return "", errors.New("patch target is not UTF-8")
			}
			parent, err := os.Stat(filepath.Dir(target))
			if err != nil {
				return "", err
			}
			edit.original = &patchOriginal{file: info, parent: parent, data: original}
			content := textLines(string(original))
			cursor, hunks := 0, 0
			if index < len(lines) && strings.HasPrefix(lines[index], "*** Move to: ") {
				destination, err := patchPath(root, strings.TrimPrefix(lines[index], "*** Move to: "))
				if err != nil {
					return "", err
				}
				if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					return "", errors.New("move target exists")
				}
				edit.destination = destination
				index++
			}
			for index < len(lines) && !strings.HasPrefix(lines[index], "*** ") {
				line := lines[index]
				if line != "@@" && !strings.HasPrefix(line, "@@ ") {
					return "", errors.New("expected @@ hunk")
				}
				if anchor, ok := strings.CutPrefix(line, "@@ "); ok {
					found := -1
					for at := cursor; at < len(content); at++ {
						if content[at] == anchor {
							found = at + 1
							break
						}
					}
					if found < 0 {
						return "", errors.New("hunk anchor missing")
					}
					cursor = found
				}
				index++
				old, newLines := []string{}, []string{}
				for index < len(lines) && !strings.HasPrefix(lines[index], "@@") && !strings.HasPrefix(lines[index], "*** ") {
					line = lines[index]
					index++
					prefix, body := byte(' '), ""
					if line != "" {
						prefix, body = line[0], line[1:]
					}
					switch prefix {
					case ' ':
						old = append(old, body)
						newLines = append(newLines, body)
					case '-':
						old = append(old, body)
					case '+':
						newLines = append(newLines, body)
					default:
						return "", errors.New("invalid patch line")
					}
				}
				eof := index < len(lines) && lines[index] == "*** End of File"
				if eof {
					index++
				}
				if len(old) == 0 && len(newLines) == 0 {
					return "", errors.New("empty hunk")
				}
				at := -1
				if len(old) == 0 {
					at = cursor
					if eof {
						at = len(content)
					}
				} else {
					for n := cursor; n+len(old) <= len(content); n++ {
						match := true
						for k := range old {
							if content[n+k] != old[k] {
								match = false
								break
							}
						}
						if match && (!eof || n+len(old) == len(content)) {
							at = n
							break
						}
					}
				}
				if at < 0 || at > len(content) {
					return "", fmt.Errorf("patch context does not match %s", target)
				}
				updated := append([]string{}, content[:at]...)
				updated = append(updated, newLines...)
				updated = append(updated, content[at+len(old):]...)
				content = updated
				cursor = at + len(newLines)
				hunks++
			}
			if hunks == 0 {
				return "", errors.New("update requires a hunk")
			}
			value := ""
			if len(content) > 0 {
				value = strings.Join(content, "\n") + "\n"
			}
			edit.content = &value
		default:
			return "", fmt.Errorf("invalid patch header: %s", header)
		}
		edits = append(edits, edit)
	}
	if len(edits) == 0 {
		return "", errors.New("empty patch")
	}
	seen := map[string]bool{}
	for _, edit := range edits {
		for _, path := range []string{edit.path, edit.destination} {
			if path == "" {
				continue
			}
			if seen[path] {
				return "", errors.New("duplicate patch path")
			}
			seen[path] = true
		}
	}
	report := []string{}
	for _, edit := range edits {
		// Recheck before mutation because another tool may have changed the workspace
		// since preflight. The workspace is not a security boundary against its owner.
		if _, err := patchPath(root, edit.path); err != nil {
			return strings.Join(report, "\n"), err
		}
		if edit.content == nil {
			if err := os.Remove(edit.path); err != nil {
				return strings.Join(report, "\n"), err
			}
			report = append(report, "Deleted "+edit.path)
			continue
		}
		target := edit.path
		if edit.destination != "" {
			target = edit.destination
			if _, err := patchPath(root, target); err != nil {
				return strings.Join(report, "\n"), err
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return strings.Join(report, "\n"), err
		}
		original := edit.original
		if edit.destination != "" {
			original = nil
		}
		if err := writeAtomic(target, []byte(*edit.content), edit.mode, original); err != nil {
			return strings.Join(report, "\n"), err
		}
		if edit.destination != "" {
			if err := os.Remove(edit.path); err != nil {
				return strings.Join(report, "\n"), err
			}
		}
		report = append(report, "Updated "+target)
	}
	return strings.Join(report, "\n"), nil
}
func writeAtomic(path string, data []byte, mode os.FileMode, original *patchOriginal) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".pot-patch-")
	if err != nil {
		if patchTempDenied(err) && original != nil {
			return writePatchInPlace(path, data, original)
		}
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		// Linux exact-file write grants are bind mounts. Their hidden parent
		// can accept a private temporary file, but the mount cannot be replaced.
		if patchMountBusy(err) && original != nil {
			return writePatchInPlace(path, data, original)
		}
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
