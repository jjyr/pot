package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"github.com/jjyr/pot/pkg/privatefs"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unsafe"
)

func TestWindowsCredentialPersistenceAndMissingKey(t *testing.T) {
	ctx := context.Background()
	account := "test-" + rand.Text()
	target, _ := windows.UTF16PtrFromString("pot.control.secrets/" + account)
	defer credDLL.NewProc("CredDeleteW").Call(uintptr(unsafe.Pointer(target)), 1, 0)
	if _, err := platformKey(ctx, account, false); err == nil {
		t.Fatal("missing credential was regenerated")
	}
	first, err := platformKey(ctx, account, true)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(first)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			key, e := platformKey(ctx, account, true)
			defer clear(key)
			if e != nil || !bytes.Equal(key, first) {
				t.Errorf("concurrent credential changed: %v", e)
			}
		})
	}
	wg.Wait()
	key, err := platformKey(ctx, account, false)
	defer clear(key)
	if err != nil || !bytes.Equal(key, first) {
		t.Fatal("credential changed on reopen", err)
	}
}
func TestWindowsEncryptedStoreRestart(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "private")
	if err := privatefs.Mkdir(dir); err != nil {
		t.Fatal(err)
	}
	account := "test-" + rand.Text()
	target, _ := windows.UTF16PtrFromString("pot.control.secrets/" + account)
	defer credDLL.NewProc("CredDeleteW").Call(uintptr(unsafe.Pointer(target)), 1, 0)
	path := filepath.Join(dir, "control.sqlite")
	store, err := Open(path, SystemKey{Account: account})
	if err != nil {
		t.Fatal(err)
	}
	value := []byte("windows-test-token")
	if err = store.Put(ctx, "credential", value); err != nil {
		t.Fatal(err)
	}
	store.Close()
	body, err := os.ReadFile(path)
	if err != nil || bytes.Contains(body, value) {
		t.Fatal("plaintext credential persisted", err)
	}
	store, err = Open(path, SystemKey{Account: account})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	read, err := store.Get(ctx, "credential")
	if err != nil || !bytes.Equal(read, value) {
		t.Fatal("encrypted restart failed", err)
	}
}
