package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsShellDefaultsAndReservedEnvironment(t *testing.T) {
	s := DefaultShellSettings()
	if !filepath.IsAbs(s.Executable) || !strings.EqualFold(filepath.Base(s.Executable), "powershell.exe") || s.AllowLoginShell || s.ShellSnapshot {
		t.Fatal(s)
	}
	for _, name := range []string{"SYSTEMROOT", "COMSPEC", "PATHEXT", "PSMODULEPATH", "PSEXECUTIONPOLICYPREFERENCE", "USERPROFILE", "TEMP"} {
		if CredentialEnvironmentName(name) {
			t.Fatalf("loader/startup variable %s accepted", name)
		}
	}
	if !CredentialEnvironmentName("SERVICE_TOKEN") {
		t.Fatal("service credential rejected")
	}
}
