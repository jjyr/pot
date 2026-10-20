package nodeidentity

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStableAutonomousIdentityAndLocalLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machines.sqlite")
	d, err := Open(path, Options{RuntimeID: "node"})
	if err != nil {
		t.Fatal(err)
	}
	identity, key := d.TransportIdentity()
	if err = d.SetLabels(context.Background(), "another", []string{"remote"}); err == nil {
		t.Fatal("changed foreign node labels")
	}
	if err = d.SetLabels(context.Background(), "node", []string{"local", "desktop", "desktop"}); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path, Options{RuntimeID: "node"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	next, nextKey := d.TransportIdentity()
	if identity != next || !reflect.DeepEqual(key, nextKey) {
		t.Fatal("signing identity changed")
	}
	if !reflect.DeepEqual(d.LocalLabels(), []string{"local", "desktop"}) {
		t.Fatal(d.LocalLabels())
	}
	var live int
	if err = d.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='controlplane_state'").Scan(&live); err != nil || live != 0 {
		t.Fatal("live membership table retained", live, err)
	}
}
