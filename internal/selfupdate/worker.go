package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/storage"
)

type Job struct {
	Install   string       `json:"install"`
	Directory string       `json:"directory"`
	Version   string       `json:"version"`
	Interrupt bool         `json:"interrupt"`
	Package   Installation `json:"package"`
	Retry     bool         `json:"retry"`
}

func Worker(path string) (result error) {
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
	// Recheck eligibility in the independent process as well as in OpenHost.
	// Formula installations deliberately omit this self-update registration.
	if err = checkInstallation(job.Install, job.Package); err != nil {
		return err
	}
	if err = privateDirectory(job.Directory); err != nil {
		return err
	}
	lock, err := openLock(filepath.Join(job.Directory, "update.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
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
	if job.Retry {
		return retryInstalled(job, save)
	}
	peers, err := participants(job.Directory)
	if err != nil {
		save("failed", err)
		return err
	}
	if len(peers) == 0 {
		save("failed", errors.New("no registered update hosts"))
		return errors.New("no registered update hosts")
	}
	hosts, err := restartHosts(job.Install, peers)
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
		for _, host := range hosts {
			if err := host.resume(); err != nil {
				save("blocked", err)
				result = errors.Join(result, err)
			}
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
	feed, err := os.ReadFile(filepath.Join(job.Directory, "verified-feed.json"))
	if err != nil {
		return err
	}
	release, err := Verify(feed, buildinfo.UpdatePublicKey)
	if err != nil {
		return err
	}
	if release.Version != job.Version || eligibility(release) != "" {
		return errors.New("prepared release is not eligible")
	}
	if err := checkTarget(prepared.Path, job.Package, release); err != nil {
		return err
	}
	sourceSchemas, err := storage.Schemas()
	if err != nil {
		return err
	}
	schemaChange := !reflect.DeepEqual(sourceSchemas, release.Schemas)
	// Ask every host to close its servers, stores and execution scopes, and wait
	// for its full shutdown result. A timeout or uncertain cleanup aborts install.
	for _, host := range hosts {
		if err = host.prepare(job.Directory); err != nil {
			if host.command.App {
				state.Phase = "blocked"
			}
			return fmt.Errorf("cannot stop local host: %w", err)
		}
	}
	exclusive, err := openLock(filepath.Join(job.Directory, "installation.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
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
	rootLocks, err := lockDataRoots(peers)
	if err != nil {
		return err
	}
	defer closeLocks(rootLocks)
	journal, err := migrationPreflight(peers, release)
	if err != nil {
		return err
	}
	if err := journal.save(job.Directory, "prepared"); err != nil {
		return err
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
	if err := journal.save(job.Directory, "restarting"); err != nil {
		state.Phase = "blocked"
		return err
	}
	save("restarting", nil)
	closeLocks(rootLocks)
	exclusive.Close()
	for _, host := range hosts {
		if err = host.resume(); err != nil {
			break
		}
	}
	if err == nil {
		err = healthy(hosts, job.Directory, job.Version)
	}
	if err == nil {
		err = journal.complete()
	}
	if err == nil {
		if err := journal.save(job.Directory, "complete"); err != nil {
			state.Phase = "blocked"
			return err
		}
		save("current", nil)
		return nil
	}
	// No business admission is allowed while restarting. Stop new hosts before
	// restoring the previous complete package; retain all original task receipts.
	for _, host := range hosts {
		if stopErr := host.prepare(job.Directory); stopErr != nil {
			state.Phase = "blocked"
			return errors.Join(err, errors.New("rollback blocked: new host cleanup unconfirmed"))
		}
	}
	exclusive, lockErr := openLock(filepath.Join(job.Directory, "installation.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
	if lockErr != nil {
		state.Phase = "blocked"
		return errors.Join(err, lockErr)
	}
	defer exclusive.Close()
	rollbackLocks, lockErr := lockDataRoots(peers)
	if lockErr != nil {
		state.Phase = "blocked"
		return errors.Join(err, lockErr)
	}
	defer closeLocks(rollbackLocks)
	if schemaChange {
		if changed := journal.unchanged(); changed != nil {
			state.Phase = "blocked"
			_ = journal.save(job.Directory, "forward_recovery_required")
			return errors.Join(err, errors.New("automatic rollback refused after committed or uncertain migrations; inspect migration-journal.json and worker.log; repair/restart this new version, never start the old binary"), changed)
		}
	}
	if e := journal.save(job.Directory, "rolling_back"); e != nil {
		state.Phase = "blocked"
		return errors.Join(err, e)
	}
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

func checkInstallation(install string, expected Installation) error {
	metadata := filepath.Join(install, "Resources/update.json")
	if expected.Kind == "app" {
		metadata = filepath.Join(install, "Contents/Resources/update.json")
	} else if expected.Kind != "cli" {
		return errors.New("invalid update installation kind")
	}
	data, err := os.ReadFile(metadata)
	if err != nil {
		return err
	}
	var installed Installation
	if err = json.Unmarshal(data, &installed); err != nil {
		return err
	}
	if installed != expected || installed.Protocol != UpdateProtocol || installed.StorageFormat != storageFormat() {
		return errors.New("update job does not match installed package")
	}
	return nil
}

func healthy(hosts []*restartHost, directory, version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), healthTimeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok := true
		for _, host := range hosts {
			if host.command.App {
				if host.childDone != nil {
					select {
					case <-host.childDone:
						return errors.New("updated native App exited before readiness")
					default:
					}
				}
				if err := host.discover(directory); err != nil {
					ok = false
					break
				}
			}
			var current Participant
			if err := localCall(ctx, host.Socket, "GET", "/status", &current); err != nil || current.BootID == "" || current.BootID == host.previousBoot || !current.Ready || current.Protocol != UpdateProtocol || current.Version != version || current.Root != host.Root {
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

func checkTarget(path string, source Installation, release Release) error {
	found := false
	for _, artifact := range release.Artifacts {
		if artifact.Kind == source.Kind && artifact.Platform == source.Platform {
			found = true
		}
	}
	if !found {
		return errors.New("authenticated release has no artifact for this installation")
	}
	metadata := filepath.Join(path, "Resources/update.json")
	if source.Kind == "app" {
		metadata = filepath.Join(path, "Contents/Resources/update.json")
	}
	body, err := os.ReadFile(metadata)
	if err != nil {
		return err
	}
	var target Installation
	if err := json.Unmarshal(body, &target); err != nil {
		return err
	}
	if target.Version != release.Version || target.Protocol != UpdateProtocol || target.StorageFormat != release.StorageFormat || target.Kind != source.Kind || target.Platform != source.Platform {
		return errors.New("prepared package does not match authenticated target")
	}
	return nil
}
