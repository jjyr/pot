package control

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// These are product-owned authentication identities, not directory exclusions or
// filename heuristics. Stat only: never read authentication bytes to classify a
// file. Custom opaque KeySource implementations cannot expose a file location.
func (s *Service) messageAuthenticationFiles() []string {
	paths := []string{filepath.Join(s.options.Root, protocol.RuntimeEndpointFile)}
	// The node signing private key is stored unencrypted in this exact database.
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		paths = append(paths, filepath.Join(s.options.Root, "machines.sqlite")+suffix)
	}
	switch key := s.options.Keys.(type) {
	case secrets.FileKey:
		paths = append(paths, key.Path)
	case *secrets.FileKey:
		paths = append(paths, key.Path)
	case secrets.SystemKey, *secrets.SystemKey:
		if path, ok := os.LookupEnv("POT_SECRET_KEY_FILE"); ok {
			paths = append(paths, path)
		} else if runtime.GOOS != "darwin" {
			if home, err := os.UserHomeDir(); err == nil {
				paths = append(paths, filepath.Join(home, ".pot", "secrets", "master.key"))
			}
		}
	}
	return paths
}

func (s *Service) messageAuthenticationGuard() func(string, os.FileInfo) error {
	var paths []string
	var identities []os.FileInfo
	for _, path := range s.messageAuthenticationFiles() {
		if absolute, err := filepath.Abs(path); err == nil && path != "" {
			paths = append(paths, absolute)
			if info, err := os.Stat(absolute); err == nil {
				identities = append(identities, info)
			}
		}
	}
	return func(path string, info os.FileInfo) error {
		// Retain initial identities as well as current ones: replacing a known
		// credential path during a read must not free its old hard-link aliases.
		for _, secret := range identities {
			if os.SameFile(info, secret) {
				return errFileForbidden
			}
		}
		for _, denied := range paths {
			if filepath.Clean(path) == denied {
				return errFileForbidden
			}
			// Check the opened descriptor before reading any bytes, including
			// hard links and symlinked parent directories.
			if secret, err := os.Stat(denied); err == nil && os.SameFile(info, secret) {
				return errFileForbidden
			}
		}
		return nil
	}
}

func (s *Service) snapshotLocalMessageFile(ctx context.Context, ref resources.MessageFile) (*resources.Resource, error) {
	src := ref.Source
	path := ref.Href
	if match := fileLine.FindStringSubmatchIndex(path); match != nil {
		if _, err := strconv.ParseUint(path[match[2]:match[3]], 10, 32); err != nil {
			return nil, errFileForbidden
		}
		path = path[:match[0]]
	}
	taskLink := strings.HasPrefix(path, "pot-file://")
	if taskLink {
		u, err := url.Parse(path)
		if err != nil || !core.Identifier(u.Host) || u.Host != src.Task || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, errFileForbidden
		}
		path = strings.TrimPrefix(u.Path, "/")
		if !protocol.ValidFilePath(path) {
			return nil, errFileForbidden
		}
	} else {
		var err error
		path, err = url.PathUnescape(path)
		if err != nil || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) {
			return nil, errFileForbidden
		}
	}
	if !filepath.IsAbs(src.Root) || src.RootID == "" {
		return nil, errFileForbidden
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(src.Root, path)
	}
	path = filepath.Clean(path)
	// Resolve the receipt before touching mutable paths. Ready snapshots remain
	// available even after their source directory or symlink target disappears.
	// Task references carry stronger identity/path constraints than ordinary
	// links; a successful ordinary-link receipt must never satisfy a Task link.
	sourceIdentity := any(src)
	if len(ref.ReceiptSource) != 0 {
		sourceIdentity = ref.ReceiptSource
	}
	hash, _ := core.HashJSON([]any{sourceIdentity, taskLink, path, ref.Message, ref.Revision})
	request := "message-file-" + hash
	if previous, err := s.Resources.Receipt(ctx, src.Owner, request, hash); err != nil || previous != nil {
		return previous, err
	}
	// Pre-existing confined snapshots used root-relative receipt identities. This
	// lookup only recovers saved bytes; every fresh read uses the same policy.
	legacyRoot := src.Root
	if !taskLink && src.Task == "" && path == src.Config && src.Config == filepath.Join(s.Registry.Directory(src.Owner), "AGENTS.md") {
		legacyRoot = filepath.Dir(src.Config)
	}
	if pathWithin(path, legacyRoot) {
		if relative, err := filepath.Rel(legacyRoot, path); err == nil && protocol.ValidFilePath(filepath.ToSlash(relative)) {
			legacyHash, _ := core.HashJSON([]any{sourceIdentity, legacyRoot, filepath.ToSlash(relative), ref.Message, ref.Revision})
			if previous, err := s.Resources.Receipt(ctx, src.Owner, "message-file-"+legacyHash, legacyHash); err != nil || previous != nil {
				return previous, err
			}
		}
	}
	authenticationGuard := s.messageAuthenticationGuard()
	if src.Task != "" {
		r, err := s.Tasks.Get(ctx, src.Owner, src.Task)
		if err != nil || r == nil {
			return nil, errFileForbidden
		}
		original, err := s.Tasks.Run(ctx, src.Owner, src.Task, src.Run)
		if err != nil || original == nil || original.Launch == nil || original.Launch.ExecutionTarget.RuntimeID != src.Node {
			return nil, protocol.APIError{Code: "original_execution_unavailable"}
		}
		workspace, err := s.RunJournal.TaskWorkspace(ctx, src.Task)
		if err != nil || workspace == nil {
			return nil, protocol.APIError{Code: "original_execution_unavailable"}
		}
		if workspace.Path != src.Root || workspace.DirectoryID != src.RootID {
			return nil, errFileForbidden
		}
	}
	// Workspace identity is still execution provenance, including for absolute
	// references. It does not limit the target of an ordinary file link.
	var origin *os.Root
	var err error
	if src.Task != "" {
		origin, err = s.openTaskWorkspace(ctx, src.Task)
	} else {
		origin, err = os.OpenRoot(src.Root)
	}
	if err != nil {
		return nil, err
	}
	defer origin.Close()
	info, err := origin.Stat(".")
	if err != nil {
		return nil, err
	}
	if id, err := taskDirectoryIdentity(info); err != nil || id != src.RootID {
		return nil, errFileForbidden
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	base, err := filepath.EvalSymlinks(src.Root)
	if err != nil {
		return nil, err
	}
	if !pathWithin(canonical, base) {
		if taskLink {
			return nil, errFileForbidden
		}
		base = filepath.Dir(canonical)
	}
	entry, err := filepath.Rel(base, canonical)
	if err != nil {
		return nil, errFileForbidden
	}
	// Choose the base after resolving the explicit entry. Keeping a handle to
	// that base confines implicit dependencies even during symlink races.
	baseInfo, err := os.Stat(base)
	if err != nil {
		return nil, err
	}
	disk, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer disk.Close()
	openedBase, err := disk.Stat(".")
	if err != nil || !os.SameFile(baseInfo, openedBase) {
		return nil, errFileChanged
	}
	type stamp struct{ target, revision string }
	stamps := map[string]stamp{}
	read := func(name string, limit int) ([]byte, error) {
		candidate := filepath.Join(base, filepath.FromSlash(name))
		target, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return nil, err
		}
		// HTML may bundle only dependencies under its chosen base. Broad sharing
		// applies to explicitly linked entry files, not implicit dependencies.
		if !pathWithin(target, base) {
			return nil, errFileForbidden
		}
		relative, err := filepath.Rel(base, target)
		if err != nil {
			return nil, errFileForbidden
		}
		before, err := disk.Stat(relative)
		if err != nil {
			return nil, err
		}
		revision, err := taskFileRevision(before)
		if err != nil {
			return nil, err
		}
		data, err := readBoundRoot(ctx, disk, relative, limit, revision, func(info os.FileInfo) error {
			return authenticationGuard(target, info)
		})
		if err != nil {
			return nil, err
		}
		stamps[name] = stamp{target, revision}
		return data, nil
	}
	verify := func() error {
		currentBase, err := os.Stat(base)
		if err != nil || !os.SameFile(baseInfo, currentBase) {
			return errFileChanged
		}
		info, err := os.Stat(src.Root)
		if err != nil {
			return err
		}
		if id, err := taskDirectoryIdentity(info); err != nil || id != src.RootID {
			return errFileForbidden
		}
		current, err := filepath.EvalSymlinks(path)
		if err != nil || current != canonical {
			return errFileChanged
		}
		for name, stamp := range stamps {
			target, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(name)))
			if err != nil || target != stamp.target {
				return errFileChanged
			}
			info, err := os.Stat(target)
			if err != nil {
				return err
			}
			if revision, err := taskFileRevision(info); err != nil || revision != stamp.revision {
				return errFileChanged
			}
			if err := authenticationGuard(target, info); err != nil {
				return err
			}
		}
		return nil
	}
	return s.registerMessageSnapshot(ctx, ref, request, hash, filepath.ToSlash(entry), read, verify)
}
