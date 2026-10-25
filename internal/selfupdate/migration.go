package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/storage"
)

// The journal stores only version evidence, never database copies or secrets.
// Its presence before replacement fences crashes, including partial multi-root
// commits. A missing/uncertain ledger cannot authorize a package downgrade.
type migrationJournal struct {
	Protocol int                                `json:"protocol"`
	Version  string                             `json:"version"`
	Stage    string                             `json:"stage"`
	Roots    map[string][]storage.DatabaseState `json:"roots"`
	Target   storage.SchemaCatalog              `json:"target"`
}

func lockDataRoots(peers []Participant) ([]*os.File, error) {
	locks := []*os.File{}
	seen := map[string]bool{}
	for _, peer := range peers {
		if peer.Root == "" || seen[peer.Root] {
			continue
		}
		seen[peer.Root] = true
		info, err := os.Lstat(peer.Root)
		if err != nil || !filepath.IsAbs(peer.Root) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			closeLocks(locks)
			return nil, fmt.Errorf("invalid private data root %s", peer.Root)
		}
		for _, name := range []string{"runtime.lock", "web-ui-server.lock"} {
			lock, err := openLock(filepath.Join(peer.Root, name), syscall.LOCK_EX|syscall.LOCK_NB)
			if err != nil {
				closeLocks(locks)
				return nil, fmt.Errorf("data root still in use %s: %w", peer.Root, err)
			}
			locks = append(locks, lock)
		}
	}
	return locks, nil
}

func closeLocks(locks []*os.File) {
	for _, lock := range locks {
		_ = lock.Close()
	}
}

func migrationPreflight(peers []Participant, release Release) (migrationJournal, error) {
	j := migrationJournal{Protocol: UpdateProtocol, Version: release.Version, Stage: "prepared", Roots: map[string][]storage.DatabaseState{}, Target: release.Schemas}
	for _, peer := range peers {
		if peer.Protocol != UpdateProtocol {
			return j, errors.New("host update protocol requires a manual bootstrap")
		}
		if peer.Root == "" {
			continue
		}
		if _, ok := j.Roots[peer.Root]; ok {
			continue
		}
		states, err := storage.InspectRoot(context.Background(), peer.Root)
		if err != nil {
			return j, err
		}
		j.Roots[peer.Root] = states
	}
	return j, nil
}

func (j *migrationJournal) save(directory, stage string) error {
	j.Stage = stage
	return instance.WriteJSON(filepath.Join(directory, "migration-journal.json"), j)
}

func (j migrationJournal) unchanged() error {
	for root, before := range j.Roots {
		after, err := storage.InspectRoot(context.Background(), root)
		if err != nil {
			return fmt.Errorf("%s: cannot prove old schema: %w", root, err)
		}
		if !reflect.DeepEqual(before, after) {
			return fmt.Errorf("%s: migration ledger changed; keep the new package and retry forward", root)
		}
	}
	return nil
}

func (j migrationJournal) complete() error {
	for root := range j.Roots {
		states, err := storage.InspectRootSchemas(context.Background(), root, j.Target)
		if err != nil {
			return err
		}
		for _, state := range states {
			if !reflect.DeepEqual(state.History, j.Target[state.Kind]) {
				return fmt.Errorf("%s: target migrations incomplete", state.Path)
			}
		}
	}
	return nil
}
