package main

import (
	"fmt"
	"github.com/jjyr/pot/pkg/sandbox"
	"github.com/jjyr/pot/pkg/sandboxprovider"
	local "github.com/jjyr/pot/plugins/internal/local"
	"os"
	"path/filepath"
)

func main() {
	err := sandboxprovider.Serve(func(c sandboxprovider.Config) (sandbox.Provider, error) {
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		return local.NewAppleVM(local.AppleVMConfig{Helper: filepath.Join(filepath.Dir(executable), "PotMacOSHelper"), UserSkills: c.UserSkills, BuiltinSkills: c.BuiltinSkills}), nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
