package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/pkg/storage"
)

// Retry keeps the installed target. It never selects the previous package or
// replays interrupted work. Missing hosts or uncertain cleanup stay blocked.
func retryInstalled(job Job, save func(string, error)) (result error) {
	defer func() {
		if result != nil {
			save("blocked", result)
		}
	}()
	var journal migrationJournal
	body, err := os.ReadFile(filepath.Join(job.Directory, "migration-journal.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, &journal); err != nil {
		return err
	}
	schemas, err := storage.Schemas()
	if err != nil {
		return err
	}
	if journal.Protocol != UpdateProtocol || journal.Version != job.Version || job.Version != buildinfo.Version || !reflect.DeepEqual(journal.Target, schemas) {
		return errors.New("retry requires the installed target binary and matching migration journal")
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
		return errors.New("retry target is not authenticated")
	}
	if err := checkTarget(job.Install, job.Package, release); err != nil {
		return err
	}
	peers, err := participants(job.Directory)
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		return errors.New("retry requires all expected hosts; start the installed new version after inspecting worker.log")
	}
	seen := map[string]bool{}
	for _, peer := range peers {
		if peer.Version != job.Version || peer.Protocol != UpdateProtocol || peer.Work.Active() {
			return errors.New("retry requires stopped target-version hosts")
		}
		seen[peer.Root] = true
	}
	for root := range journal.Roots {
		if !seen[root] {
			return errors.New("retry is missing an expected data-root host")
		}
	}
	hosts, err := restartHosts(job.Install, peers)
	if err != nil {
		return err
	}
	for _, host := range hosts {
		if err := host.prepare(job.Directory); err != nil {
			return err
		}
	}
	exclusive, err := openLock(filepath.Join(job.Directory, "installation.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return err
	}
	defer exclusive.Close()
	locks, err := lockDataRoots(peers)
	if err != nil {
		return err
	}
	defer closeLocks(locks)
	for root := range journal.Roots {
		if _, err := storage.InspectRoot(context.Background(), root); err != nil {
			return err
		}
	}
	if err := journal.save(job.Directory, "retrying_forward"); err != nil {
		return err
	}
	save("restarting", nil)
	closeLocks(locks)
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
	if err != nil {
		for _, host := range hosts {
			err = errors.Join(err, host.prepare(job.Directory))
		}
		_ = journal.save(job.Directory, "forward_recovery_required")
		return err
	}
	if err := journal.save(job.Directory, "complete"); err != nil {
		return err
	}
	save("current", nil)
	return nil
}
