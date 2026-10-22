package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/instance"
)

type Job struct {
	Install   string       `json:"install"`
	Directory string       `json:"directory"`
	Version   string       `json:"version"`
	Interrupt bool         `json:"interrupt"`
	Package   Installation `json:"package"`
}

func Worker(path string) (result error) {
	if !updatesSupported() {
		return errors.New("Windows portable packages require manual upgrades")
	}
	var job Job
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &job); err != nil {
		return err
	}
	if !filepath.IsAbs(job.Install) || job.Directory != job.Install+".updates" || path != filepath.Join(job.Directory, "job.json") || !versionPattern.MatchString(job.Version) {
		return errors.New("invalid update job")
	}
	if err = privateDirectory(job.Directory); err != nil {
		return err
	}
	lock, err := openLock(filepath.Join(job.Directory, "update.lock"), lockExclusive|lockNonblock)
	if err != nil {
		return err
	}
	defer lock.Close()
	var state State
	data, err = os.ReadFile(filepath.Join(job.Directory, "state.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Phase != "installing" || state.Version != job.Version {
		return errors.New("stale update job")
	}
	save := func(phase string, e error) {
		state.Phase = phase
		state.Error = ""
		if e != nil {
			state.Error = e.Error()
		}
		_ = instance.WriteJSON(filepath.Join(job.Directory, "state.json"), state)
	}
	peers, err := participants(job.Directory)
	if err != nil {
		save("failed", err)
		return err
	}
	defer func() {
		if result != nil {
			if state.Phase == "blocked" {
				save("blocked", result)
				return
			}
			save("failed", result)
		}
		for _, p := range peers {
			_ = localCall(context.Background(), p.Socket, "POST", "/resume", nil)
		}
	}()
	for _, p := range peers {
		if p.Unsupported {
			return errors.New("installation contains protected user state")
		}
	}
	if !job.Interrupt {
		for _, p := range peers {
			if p.Work.Active() {
				save("ready", nil)
				return nil
			}
		}
	}
	var prepared struct{ Version, Path string }
	data, err = os.ReadFile(filepath.Join(job.Directory, "prepared.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &prepared); err != nil {
		return err
	}
	stage := filepath.Join(job.Directory, "stage-"+job.Version)
	if prepared.Version != job.Version || filepath.Dir(prepared.Path) != stage {
		return errors.New("invalid prepared installation")
	}
	// Ask every host to close its servers, stores and execution scopes, and wait
	// for its full shutdown result. A timeout or uncertain cleanup aborts install.
	for _, p := range peers {
		if err = localCall(context.Background(), p.Socket, "POST", "/prepare", nil); err != nil {
			return fmt.Errorf("cannot stop local host: %w", err)
		}
	}
	exclusive, err := openLock(filepath.Join(job.Directory, "installation.lock"), lockExclusive|lockNonblock)
	if err != nil {
		return errors.New("another host still owns the installation")
	}
	defer exclusive.Close()
	backup := filepath.Join(job.Directory, "backup-"+job.Version)
	if _, err = os.Lstat(backup); err == nil {
		return errors.New("previous installation backup already exists")
	}
	if !os.IsNotExist(err) {
		return err
	}
	// The data format is unchanged. Preserve closed database/metadata snapshots for
	// inspection, but do not restore them over newer durable execution receipts.
	for index, p := range peers {
		if p.Root != "" {
			if err = backupState(p.Root, filepath.Join(job.Directory, "data-"+job.Version, fmt.Sprint(index))); err != nil {
				return err
			}
		}
	}
	if err = os.Rename(job.Install, backup); err != nil {
		return err
	}
	if err = os.Rename(prepared.Path, job.Install); err != nil {
		if restoreErr := os.Rename(backup, job.Install); restoreErr != nil {
			state.Phase = "blocked"
			return errors.Join(err, restoreErr)
		}
		return err
	}
	save("restarting", nil)
	exclusive.Close()
	for _, p := range peers {
		if err = localCall(context.Background(), p.Socket, "POST", "/resume", nil); err != nil {
			break
		}
	}
	if err == nil {
		err = healthy(peers, job.Version)
	}
	if err == nil {
		save("current", nil)
		return nil
	}
	// No business admission is allowed while restarting. Stop new hosts before
	// restoring the previous complete package; retain all original task receipts.
	for _, p := range peers {
		if stopErr := localCall(context.Background(), p.Socket, "POST", "/prepare", nil); stopErr != nil {
			state.Phase = "blocked"
			return errors.Join(err, errors.New("rollback blocked: new host cleanup unconfirmed"))
		}
	}
	exclusive, lockErr := openLock(filepath.Join(job.Directory, "installation.lock"), lockExclusive|lockNonblock)
	if lockErr != nil {
		state.Phase = "blocked"
		return errors.Join(err, lockErr)
	}
	defer exclusive.Close()
	failed := filepath.Join(job.Directory, "failed-"+job.Version)
	if e := os.Rename(job.Install, failed); e != nil {
		state.Phase = "blocked"
		return errors.Join(err, e)
	}
	if e := os.Rename(backup, job.Install); e != nil {
		state.Phase = "blocked"
		return errors.Join(err, e)
	}
	return fmt.Errorf("update rolled back: %w", err)
}

var healthTimeout = 45 * time.Second

func healthy(peers []Participant, version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), healthTimeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok := true
		for _, old := range peers {
			var current Participant
			if err := localCall(ctx, old.Socket, "GET", "/status", &current); err != nil || !current.Ready || current.Version != version || current.Root != old.Root {
				ok = false
				break
			}
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("updated services did not become healthy")
		case <-ticker.C:
		}
	}
}
func backupState(root, destination string) error {
	if err := privateDirectory(destination); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".sqlite") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("data snapshot contains non-regular metadata")
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(destination, name), data, 0600); err != nil {
			return err
		}
	}
	return nil
}
